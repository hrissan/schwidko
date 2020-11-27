package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"time"
)

type ResponseWriter interface {
	WriteStatus(statusCode int)
	WriteDate(date string)
	WriteServer(server string)
	WriteContentLength(length int64)
	WriteOtherHeader(key string, value string)
	Write([]byte) (int, error)
	// TODO UpgradeWebsocket
}

type Handler func(wr ResponseWriter, request *Request)

type Server struct {
	listener net.Listener
	handler  Handler
}

type HeaderKV struct {
	key   []byte
	value []byte
}

const (
	CONNECTION_NO_WRITE       = 0
	CONNECTION_EXPECT_STATUS  = 1
	CONNECTION_EXPECT_HEADERS = 2
	CONNECTION_EXPECT_BODY    = 3
)

type Client struct {
	server           *Server
	conn             net.Conn
	incomingBuffer   []byte
	incomingReadPos  int
	incomingWritePos int

	incomingReader io.Reader
	outgoingWriter *bufio.Writer

	request Request

	// Parser state
	parseError          string
	methodStart         int
	uriStart            int
	uriWritePos         int // Due to percent encoding, we shift uri bytes
	percent1_hex_digit  int
	queryStringStart    int
	headerKeyStart      int
	headerKeyFinish     int
	headerValueStart    int
	headerValueWritePos int // Due to continuations, we shift value bytes
	headerCMSList       bool

	// Writer state
	writerState                  int
	responseDateWritten          bool
	responseServerWritten        bool
	responseContentLengthWritten int64
	responseBytesWritten         int64
}

type Request struct {
	Method             []byte
	Path               []byte
	QueryString        []byte
	VersionMinor       int
	VersionMajor       int
	KeepAlive          bool
	ContentLength      int64
	Origin             []byte
	Host               []byte
	ContentTypeMime    []byte
	ContentTypeSuffix  []byte
	basicAuthorization []byte

	TransferEncodings       [][]byte
	TransferEncodingChunked bool
	Headers                 []HeaderKV

	ConnectionUpgrade   bool
	UpgradeWebSocket    bool
	SecWebsocketKey     []byte
	SecWebsocketVersion []byte
}

const incomingBufferSize = 4096
const outgoingBufferSize = 4096
const maxHeaderSize = 2048

func (c *Client) WriteStatus(statusCode int) {
	if c.writerState != CONNECTION_EXPECT_STATUS {
		// TODO disconnect
		return
	}
	c.outgoingWriter.WriteString("HTTP/")
	c.outgoingWriter.WriteString(strconv.Itoa(c.request.VersionMajor))
	c.outgoingWriter.WriteByte('.')
	c.outgoingWriter.WriteString(strconv.Itoa(c.request.VersionMinor))
	c.outgoingWriter.WriteByte(' ')
	c.outgoingWriter.WriteString(strconv.Itoa(statusCode))
	c.outgoingWriter.WriteByte(' ')
	c.outgoingWriter.WriteString("OK\r\n") // TODO depending on status
	c.writerState = CONNECTION_EXPECT_HEADERS
}

func (c *Client) WriteDate(date string) {
	if c.writerState == CONNECTION_EXPECT_STATUS {
		c.WriteStatus(200)
	}
	if c.writerState != CONNECTION_EXPECT_HEADERS || c.responseDateWritten {
		// TODO disconnect
		return
	}
	c.responseDateWritten = true
	c.outgoingWriter.WriteString("date: ")
	c.outgoingWriter.WriteString(date)
	c.outgoingWriter.WriteString("\r\n")
}
func (c *Client) WriteServer(server string) {
	if c.writerState == CONNECTION_EXPECT_STATUS {
		c.WriteStatus(200)
	}
	if c.writerState != CONNECTION_EXPECT_HEADERS || c.responseServerWritten {
		// TODO disconnect
		return
	}
	c.responseServerWritten = true
	c.outgoingWriter.WriteString("server: ")
	c.outgoingWriter.WriteString(server)
	c.outgoingWriter.WriteString("\r\n")
}
func (c *Client) WriteContentLength(length int64) {
	if c.writerState == CONNECTION_EXPECT_STATUS {
		c.WriteStatus(200)
	}
	if c.writerState != CONNECTION_EXPECT_HEADERS || length < 0 || c.responseContentLengthWritten >= 0 {
		// TODO disconnect
		return
	}
	c.responseContentLengthWritten = length
	c.outgoingWriter.WriteString("content-length: ")
	c.outgoingWriter.WriteString(strconv.Itoa(int(length))) // TODO 64-bit fun
	c.outgoingWriter.WriteString("\r\n")
}

func (c *Client) WriteOtherHeader(key string, value string) {
	if c.writerState == CONNECTION_EXPECT_STATUS {
		c.WriteStatus(200)
	}
	if c.writerState != CONNECTION_EXPECT_HEADERS {
		// TODO disconnect
		return
	}
	c.outgoingWriter.WriteString(key)
	c.outgoingWriter.WriteString(": ")
	c.outgoingWriter.WriteString(value)
	c.outgoingWriter.WriteString("\r\n")
}

func appendTime(b []byte, t time.Time) []byte { // Copied from net.http
	const days = "SunMonTueWedThuFriSat"
	const months = "JanFebMarAprMayJunJulAugSepOctNovDec"

	t = t.UTC()
	yy, mm, dd := t.Date()
	hh, mn, ss := t.Clock()
	day := days[3*t.Weekday():]
	mon := months[3*(mm-1):]

	return append(b,
		day[0], day[1], day[2], ',', ' ',
		byte('0'+dd/10), byte('0'+dd%10), ' ',
		mon[0], mon[1], mon[2], ' ',
		byte('0'+yy/1000), byte('0'+(yy/100)%10), byte('0'+(yy/10)%10), byte('0'+yy%10), ' ',
		byte('0'+hh/10), byte('0'+hh%10), ':',
		byte('0'+mn/10), byte('0'+mn%10), ':',
		byte('0'+ss/10), byte('0'+ss%10), ' ',
		'G', 'M', 'T')
}

func (c *Client) Write(data []byte) (int, error) {
	if c.writerState == CONNECTION_EXPECT_STATUS {
		c.WriteStatus(200)
	}
	if c.writerState == CONNECTION_EXPECT_HEADERS {
		if !c.responseServerWritten {
			c.outgoingWriter.WriteString("server: crab\r\n")
			c.responseServerWritten = true
		}
		if !c.responseDateWritten {
			// TODO cache in Server
			//dateBuf := appendTime(nil, time.Now())
			//c.outgoingWriter.WriteString("date: ")
			//c.outgoingWriter.Write(dateBuf)
			//c.outgoingWriter.WriteString("\r\n")
			c.outgoingWriter.WriteString("date: Tue, 15 Nov 2020 12:45:26 GMT\r\n")
			c.responseDateWritten = true
		}
		if c.responseContentLengthWritten < 0 { // TODO actually support chunked encoding
			log.Fatalf("Chunked body not yet supported")
			c.outgoingWriter.WriteString("transfer-encoding: chunked\r\n")
		}
		c.outgoingWriter.WriteString("\r\n")
		c.writerState = CONNECTION_EXPECT_BODY
	}
	if c.writerState != CONNECTION_EXPECT_BODY {
		// TODO disconnect
		return 0, errors.New("Unexpected body write")
	}
	if c.responseContentLengthWritten >= 0 {
		if c.responseBytesWritten+int64(len(data)) > c.responseContentLengthWritten {
			return 0, errors.New("Body overflow")
		}
		n, err := c.outgoingWriter.Write(data)
		c.responseBytesWritten += int64(n)
		if err != nil {
			// TODO disconnect
			return n, err
		}
		return n, err
	}
	log.Fatalf("Chunked body not yet supported")
	return 0, nil
}

func (c *Client) readRequest() error {
	incomingBuffer := c.incomingBuffer
	incomingReadPos := c.incomingReadPos
	incomingWritePos := c.incomingWritePos
	if incomingReadPos == incomingWritePos {
		// If possible, start reading from the buffer beginning
		incomingReadPos = 0
		incomingWritePos = 0
	}
	if incomingReadPos+maxHeaderSize > len(incomingBuffer) {
		// Inplace fragments cannot be circular, if in doubt, defragment
		incomingWritePos = copy(incomingBuffer[incomingReadPos:incomingWritePos], incomingBuffer)
		incomingReadPos = 0
	}
	headerLimit := incomingReadPos + maxHeaderSize // Always <= len(incomingBuffer)

	headers := c.request.Headers // Reuse arrays
	transferEncodings := c.request.TransferEncodings
	c.request = Request{Headers: headers[:0], TransferEncodings: transferEncodings, ContentLength: -1}
	state := METHOD_START
	for {
		if incomingReadPos == headerLimit {
			return fmt.Errorf("Too big request headers")
		}
		if incomingReadPos == incomingWritePos {
			n, err := c.incomingReader.Read(incomingBuffer[incomingWritePos:])
			if err != nil {
				return err
			}
			if n == 0 {
				continue // TODO can this happen? Why?
			}
			incomingWritePos += n
		}
		state = c.consume(incomingBuffer, incomingReadPos, state)
		if state == GOOD {
			break
		}
		if state == BAD {
			return errors.New(c.parseError)
		}
		incomingReadPos++
	}
	c.incomingReadPos = incomingReadPos
	c.incomingWritePos = incomingWritePos
	return nil
}

func (c *Client) routine() {
	for {
		err := c.readRequest()
		if err != nil {
			return
		}
		c.writerState = CONNECTION_EXPECT_STATUS
		c.responseDateWritten = false
		c.responseServerWritten = false
		c.responseBytesWritten = 0
		c.responseContentLengthWritten = -1
		c.server.handler(c, &c.request)
		// TODO - additional logic
		//wr := c.outgoingWriter
		//_, _ = wr.WriteString("HTTP/1.1 200 OK\r\n")
		//_, _ = wr.WriteString("server: crab\r\n")
		//_, _ = wr.WriteString("date: Tue, 15 Nov 2020 12:45:26 GMT\r\n")
		//_, _ = wr.WriteString("content-type: text/plain; charset=utf-8\r\n")
		//_, _ = wr.WriteString("content-length: 12\r\n")
		//_, _ = wr.WriteString("\r\n")
		//_, _ = wr.WriteString("Hello, Crab!\r\n")

		c.writerState = CONNECTION_NO_WRITE
		_ = c.outgoingWriter.Flush()
	}
}

func (s *Server) ListerAndServer(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = l
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return err
		}
		client := &Client{
			server:         s,
			conn:           conn,
			incomingBuffer: make([]byte, incomingBufferSize),
			incomingReader: conn,
			outgoingWriter: bufio.NewWriterSize(conn, outgoingBufferSize),
		}
		go client.routine()
	}
}
