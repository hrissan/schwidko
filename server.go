package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net"
	"sync/atomic"
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

var timeBuffer atomic.Value

func init() {
	timeBuffer.Store(appendTime(nil, time.Now()))
	go func() {
		time.Sleep(time.Millisecond * 500)
		timeBuffer.Store(appendTime(nil, time.Now()))
	}()
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

	incomingReader   io.Reader
	outgoingBuffer   []byte
	outgoingWritePos int
	//outgoingWriter *bufio.Writer

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

	// Debug
	noncompleteCounter int
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
	BasicAuthorization []byte

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
const eofheaderGuardSize = 2

var errOVerflow = errors.New("OVerflow")

func (c *Client) writeString(str string) error {
	if c.outgoingWritePos+len(str) > len(c.outgoingBuffer) {
		return errOVerflow
	}
	c.outgoingWritePos += copy(c.outgoingBuffer[c.outgoingWritePos:], str)
	return nil
}

func (c *Client) write(str []byte) error {
	if c.outgoingWritePos+len(str) > len(c.outgoingBuffer) {
		return errOVerflow
	}
	c.outgoingWritePos += copy(c.outgoingBuffer[c.outgoingWritePos:], str)
	return nil
}

func (c *Client) writeUint(value uint) error {
	// Use end of buffer as a scratch space.
	const BUF_SIZE = 128

	if c.outgoingWritePos+BUF_SIZE > len(c.outgoingBuffer) {
		return errOVerflow
	}
	p := len(c.outgoingBuffer)

	//if (std::is_signed<T>::value && value < 0) {
	//	do {
	//		buffer[--p] = '0' - (value % 10);
	//		value /= 10;
	//	} while (value != 0 && p != 1);  // Last condition turns stack corruption into harmless wrong result
	//	buffer[--p] = '-';
	//} else {
	for {
		p--
		c.outgoingBuffer[p] = '0' + byte(value%10)
		value /= 10
		if value == 0 || p == 0 {
			break
		}
	}
	c.outgoingWritePos += copy(c.outgoingBuffer[c.outgoingWritePos:], c.outgoingBuffer[p:])
	return nil
}

func (c *Client) flush() error {
	if c.outgoingWritePos != 0 {
		_, err := c.conn.Write(c.outgoingBuffer[:c.outgoingWritePos])
		if err != nil {
			return err
		}
		c.outgoingWritePos = 0
	}
	return nil
}

func (c *Client) writeByte(value byte) error {
	if c.outgoingWritePos+1 > len(c.outgoingBuffer) {
		return errOVerflow
	}
	c.outgoingBuffer[c.outgoingWritePos] = value
	c.outgoingWritePos++
	return nil
}

func (c *Client) WriteStatus(statusCode int) {
	if c.writerState != CONNECTION_EXPECT_STATUS {
		// TODO disconnect
		return
	}
	c.writeString("HTTP/")
	c.writeUint(uint(c.request.VersionMajor))
	c.writeByte('.')
	c.writeUint(uint(c.request.VersionMinor))
	c.writeByte(' ')
	c.writeUint(uint(statusCode))
	c.writeByte(' ')
	c.writeString("OK\r\n") // TODO depending on status
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
	c.writeString("date: ")
	c.writeString(date)
	c.writeString("\r\n")
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
	c.writeString("server: ")
	c.writeString(server)
	c.writeString("\r\n")
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
	c.writeString("content-length: ")
	c.writeUint(uint(int(length))) // TODO 64-bit fun
	c.writeString("\r\n")
}

func (c *Client) WriteOtherHeader(key string, value string) {
	if c.writerState == CONNECTION_EXPECT_STATUS {
		c.WriteStatus(200)
	}
	if c.writerState != CONNECTION_EXPECT_HEADERS {
		// TODO disconnect
		return
	}
	c.writeString(key)
	c.writeString(": ")
	c.writeString(value)
	c.writeString("\r\n")
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
			c.writeString("server: crab\r\n")
			c.responseServerWritten = true
		}
		if !c.responseDateWritten {
			// TODO cache in Server
			//dateBuf := appendTime(nil, time.Now())
			c.writeString("date: ")
			dateBuf := timeBuffer.Load()
			c.write(dateBuf.([]byte))
			c.writeString("\r\n")
			//c.writeString("date: Tue, 15 Nov 2020 12:45:26 GMT\r\n")
			c.responseDateWritten = true
		}
		if c.responseContentLengthWritten < 0 { // TODO actually support chunked encoding
			log.Fatalf("Chunked body not yet supported")
			c.writeString("transfer-encoding: chunked\r\n")
		}
		c.writeString("\r\n")
		c.writerState = CONNECTION_EXPECT_BODY
	}
	if c.writerState != CONNECTION_EXPECT_BODY {
		// TODO disconnect
		return 0, errors.New("Unexpected body write")
	}
	origLen := len(data)
	if c.responseContentLengthWritten >= 0 {
		if c.responseBytesWritten+int64(len(data)) > c.responseContentLengthWritten {
			return 0, errors.New("Body overflow")
		}
		for {
			if len(data) == 0 {
				c.responseBytesWritten += int64(origLen)
				return origLen, nil
			}
			copied := copy(c.outgoingBuffer[c.outgoingWritePos:], data)
			c.outgoingWritePos += copied
			data = data[copied:]
			if c.outgoingWritePos == len(c.outgoingBuffer) {
				_, err := c.conn.Write(c.outgoingBuffer)
				if err != nil {
					// TODO disconnect
					return 0, err
				}
				c.outgoingWritePos = 0
			}
		}
	}
	log.Fatalf("Chunked body not yet supported")
	return 0, nil
}

func (c *Client) complete(rp int, wp int) bool {
	ib := c.incomingBuffer
	if wp > rp+maxHeaderSize {
		wp = rp + maxHeaderSize // Do not look beyond maxHeaderSize
	}

	for rp < wp {
		np := bytes.IndexByte(ib[rp:wp], '\n')
		if np < 0 {
			// xxxx
			return false
		}
		np += rp
		if np+2 < wp {
			// xxxxNxy
			if ib[np+1] == '\n' {
				// xxxxNNy
				return true
			}
			if ib[np+1] == '\r' && ib[np+2] == '\n' {
				// xxxxNRN
				return true
			}
			rp = np + 2
			continue
		}
		if np+2 == wp {
			// xxxxNx
			if ib[wp+1] == '\n' {
				// xxxxNN
				return true
			}
			return false
		}
		return false
	}
	return false
	/*
		ret_cnt := 0
		for rp < wp {
			input := ib[rp]
			if input != '\r' && input != '\n' {
				rp++
				ret_cnt = 0
				continue
			}
			if input == '\n' {
				rp++
				ret_cnt++
			} else {
				rp++
				input2 := ib[rp]
				if input2 != '\n' {
					return false // malformed
				}
				rp++
				ret_cnt++
			}
			if ret_cnt == 2 {
				return true
			}
		}
		return false */
}

func (c *Client) readComplete() error {
	incomingBuffer := c.incomingBuffer
	//incomingReadPos := c.incomingReadPos
	//incomingWritePos := c.incomingWritePos
	if c.incomingReadPos == c.incomingWritePos {
		// If possible, start reading from the buffer beginning
		c.incomingReadPos = 0
		c.incomingWritePos = 0
		//  []xxxxxx
	} else {
		//  xxx[xxxxxxxx]xxxxx
		//     [     ] <- maxHeaderSize
		if c.complete(c.incomingReadPos, c.incomingWritePos) {
			// do not care if it is at the end of buffer if it is complete
			return nil
		}
		if c.incomingWritePos >= c.incomingReadPos+maxHeaderSize {
			return errors.New("Incomplete header of max size")
		}
		if c.incomingReadPos+maxHeaderSize > len(incomingBuffer) {
			// Inplace fragments cannot be circular, if in doubt, defragment
			c.incomingWritePos = copy(incomingBuffer[c.incomingReadPos:c.incomingWritePos], incomingBuffer)
			c.incomingReadPos = 0
		}
	}
	// Here buffer is always checked for completeness, incomplete and less than maxHeaderSize
	// xxx[ccccc]xxxxx  // c is checked for completeness

	for {
		n, err := c.incomingReader.Read(c.incomingBuffer[c.incomingWritePos:])
		if err != nil {
			return err
		}
		if n == 0 {
			log.Panicf("connection Read returned 0 bytes for slice of %d..%d bytes", c.incomingWritePos, len(c.incomingBuffer))
		}
		// xxx[cccccnnnnn]xxxxx  // c is checked for completeness, n is not
		checkFrom := c.incomingWritePos
		if checkFrom < c.incomingReadPos+3 {
			checkFrom = c.incomingReadPos
		}
		c.incomingWritePos += n
		if c.complete(checkFrom, c.incomingWritePos) {
			return nil
		}
		if c.incomingWritePos >= c.incomingReadPos+maxHeaderSize {
			return errors.New("Incomplete header of max size")
		}
	}
}

func (c *Client) readRequest() error {
	//headers := c.request.Headers[:0] // Reuse arrays
	//transferEncodings := c.request.TransferEncodings[:0]
	//c.request = Request{Headers: headers, TransferEncodings: transferEncodings, ContentLength: -1}
	r := &c.request
	r.Method = nil
	r.Path = nil
	r.QueryString = nil
	r.ContentLength = -1
	r.Origin = nil
	r.Host = nil
	r.ContentTypeMime = nil
	r.ContentTypeSuffix = nil
	r.BasicAuthorization = nil

	r.TransferEncodings = r.TransferEncodings[:0]
	r.TransferEncodingChunked = false
	r.Headers = r.Headers[:0]

	r.ConnectionUpgrade = false
	r.UpgradeWebSocket = false
	r.SecWebsocketKey = nil
	r.SecWebsocketVersion = nil

	if err := c.readComplete(); err != nil {
		return err
	}
	state := METHOD_START
	incomingReadPos := c.incomingReadPos
	incomingBuffer := c.incomingBuffer
	for {
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
		_ = c.flush()
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
			outgoingBuffer: make([]byte, outgoingBufferSize),
		}
		go client.routine()
	}
}
