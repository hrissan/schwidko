package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/valyala/fasthttp"
)

type Server struct {
	listener net.Listener
}

type HeaderKV struct {
	key   []byte
	value []byte
}

type Client struct {
	conn             net.Conn
	incomingBuffer   []byte
	incomingReadPos  int
	incomingWritePos int

	outgoingWriter *bufio.Writer

	request Request

	// Parser state
	parseError         string
	methodStart        int
	uriStart           int
	uriWritePos        int // Due to percent encoding, we shift uri bytes
	percent1_hex_digit int
	queryStringStart   int
	headerKeyStart     int
	headerKeyFinish    int
	headerValueStart   int
	headerValueFinish  int
	headerCMSList      bool
}

type Request struct {
	Method            []byte
	Path              []byte
	QueryString       []byte
	VersionMinor      int
	VersionMajor      int
	KeepAlive         bool
	ContentLength     int64
	UpgradeWebSocket  bool
	Origin            []byte
	Host              []byte
	ContentType       []byte
	ConnectionUpgrade bool

	TransferEncodings       [][]byte
	TransferEncodingChunked bool
	Headers                 []HeaderKV
}

const incomingBufferSize = 4096
const outgoingBufferSize = 4096
const maxHeaderSize = 2048

func tolower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c - 'A' + 'a' // Assumptions on character encoding
	}
	return c
}
func tolowerSlice(data []byte) {
	for i, c := range data {
		data[i] = tolower(c)
	}
}

func isdigit(c byte) bool { return c >= '0' && c <= '9' }
func is_sp(c byte) bool   { return c == ' ' || c == '\t' }
func is_char(c byte) bool { return c <= 127 }
func is_ctl(c byte) bool  { return c <= 31 || c == 127 }
func is_tspecial(c byte) bool {
	switch c {
	case '(':
		return true
	case ')':
		return true
	case '<':
		return true
	case '>':
		return true
	case '@':
		return true
	case ',':
		return true
	case ';':
		return true
	case ':':
		return true
	case '\\':
		return true
	case '"':
		return true
	case '/':
		return true
	case '[':
		return true
	case ']':
		return true
	case '?':
		return true
	case '=':
		return true
	case '{':
		return true
	case '}':
		return true
	case ' ':
		return true
	case '\t':
		return true
	}
	return false
}

func from_hex_digit(c byte) int {
	if c >= '0' && c <= '9' {
		return int(c) - '0'
	}
	if c >= 'a' && c <= 'f' {
		return int(c) - 'a' + 10
	}
	if c >= 'A' && c <= 'F' {
		return int(c) - 'A' + 10
	}
	return -1
}

const (
	METHOD_START              = iota
	METHOD_START_LF           = iota
	METHOD                    = iota
	URI_START                 = iota
	URI                       = iota
	URI_PERCENT1              = iota
	URI_PERCENT2              = iota
	URI_QUERY_STRING          = iota
	URI_ANCHOR                = iota // empty # is allowed by standard
	HTTP_VERSION_H            = iota
	HTTP_VERSION_HT           = iota
	HTTP_VERSION_HTT          = iota
	HTTP_VERSION_HTTP         = iota
	HTTP_VERSION_SLASH        = iota
	HTTP_VERSION_MAJOR_START  = iota
	HTTP_VERSION_MAJOR        = iota
	HTTP_VERSION_MINOR_START  = iota
	HTTP_VERSION_MINOR        = iota
	STATUS_LINE_CR            = iota
	STATUS_LINE_LF            = iota
	FIRST_HEADER_LINE_START   = iota
	HEADER_LINE_START         = iota
	HEADER_NAME               = iota
	HEADER_COLON              = iota
	SPACE_BEFORE_HEADER_VALUE = iota
	HEADER_VALUE              = iota
	HEADER_LF                 = iota
	FINAL_LF                  = iota
	GOOD                      = iota
	BAD                       = iota
)

var strConnection = []byte("connection")
var strTransferEncoding = []byte("transfer-encoding")

func (c *Client) consume(incomingBuffer []byte, incomingReadPos int, state int) int {
	input := incomingBuffer[incomingReadPos]
	switch state {
	case METHOD_START:
		// Skip empty lines https://tools.ietf.org/html/rfc2616#section-4.1
		if input == '\r' {
			return METHOD_START_LF
		}
		if input == '\n' {
			return METHOD
		}
		if !is_char(input) || is_ctl(input) || is_tspecial(input) {
			c.parseError = "Invalid character at method start"
			return BAD
		}
		c.methodStart = incomingReadPos
		return METHOD
	case METHOD_START_LF:
		if input != '\n' {
			c.parseError = "Invalid LF at method start"
			return BAD
		}
		return METHOD_START
	case METHOD:
		if is_sp(input) {
			c.request.Method = incomingBuffer[c.methodStart:incomingReadPos]
			return URI_START
		}
		if !is_char(input) || is_ctl(input) || is_tspecial(input) {
			c.parseError = "Invalid character in method"
			return BAD
		}
		return METHOD
	case URI_START:
		if is_sp(input) {
			return URI_START
		}
		if is_ctl(input) {
			c.parseError = "Invalid (control) character at uri start"
			return BAD
		}
		if input == '#' {
			c.parseError = "Invalid '#' character at uri start"
			return BAD
		}
		if input == '?' {
			c.parseError = "Invalid '?' character at uri start"
			return BAD
		}
		c.uriStart = incomingReadPos
		c.uriWritePos = c.uriStart + 1
		if input == '%' {
			return URI_PERCENT1
		}
		return URI
	case URI:
		if is_sp(input) {
			c.request.Path = incomingBuffer[c.uriStart:c.uriWritePos]
			return HTTP_VERSION_H
		}
		if is_ctl(input) {
			c.parseError = "Invalid (control) character in uri"
			return BAD
		}
		if input == '#' {
			c.request.Path = incomingBuffer[c.uriStart:c.uriWritePos]
			return URI_ANCHOR
		}
		if input == '?' {
			c.request.Path = incomingBuffer[c.uriStart:c.uriWritePos]
			c.queryStringStart = incomingReadPos + 1
			return URI_QUERY_STRING
		}
		if input == '%' {
			return URI_PERCENT1
		}
		incomingBuffer[c.uriWritePos] = input
		c.uriWritePos += 1
		return URI
	case URI_PERCENT1:
		c.percent1_hex_digit = from_hex_digit(input)
		if c.percent1_hex_digit < 0 {
			c.parseError = "URI percent-encoding invalid first hex digit"
			return BAD
		}
		return URI_PERCENT2
	case URI_PERCENT2:
		{
			digit2 := from_hex_digit(input)
			if digit2 < 0 {
				c.parseError = "URI percent-encoding invalid second hex digit"
				return BAD
			}
			incomingBuffer[c.uriWritePos] = byte(c.percent1_hex_digit*16 + digit2)
			c.uriWritePos += 1
			return URI
		}
	case URI_QUERY_STRING:
		if is_sp(input) {
			c.request.QueryString = incomingBuffer[c.queryStringStart:incomingReadPos]
			return HTTP_VERSION_H
		}
		if is_ctl(input) {
			c.parseError = "Invalid (control) character in uri"
			return BAD
		}
		if input == '#' {
			c.request.QueryString = incomingBuffer[c.queryStringStart:incomingReadPos]
			return URI_ANCHOR
		}
		return URI_QUERY_STRING
	case URI_ANCHOR:
		if is_sp(input) {
			return HTTP_VERSION_H
		}
		if is_ctl(input) {
			c.parseError = "Invalid (control) character in uri"
			return BAD
		}
		return URI_ANCHOR
	case HTTP_VERSION_H:
		if is_sp(input) {
			return HTTP_VERSION_H
		}
		if input != 'H' {
			c.parseError = "Invalid http version, 'H' is expected"
			return BAD
		}
		return HTTP_VERSION_HT
	case HTTP_VERSION_HT:
		if input != 'T' {
			c.parseError = "Invalid http version, 'T' is expected"
			return BAD
		}
		return HTTP_VERSION_HTT
	case HTTP_VERSION_HTT:
		if input != 'T' {
			c.parseError = "Invalid http version, 'T' is expected"
			return BAD
		}
		return HTTP_VERSION_HTTP
	case HTTP_VERSION_HTTP:
		if input != 'P' {
			c.parseError = "Invalid http version, 'P' is expected"
			return BAD
		}
		return HTTP_VERSION_SLASH
	case HTTP_VERSION_SLASH:
		if input != '/' {
			c.parseError = "Invalid http version, '/' is expected"
			return BAD
		}
		return HTTP_VERSION_MAJOR_START
	case HTTP_VERSION_MAJOR_START:
		if !isdigit(input) {
			c.parseError = "Invalid http version major start, must be digit"
			return BAD
		}
		c.request.VersionMajor = int(input) - '0'
		return HTTP_VERSION_MAJOR
	case HTTP_VERSION_MAJOR:
		if input == '.' {
			return HTTP_VERSION_MINOR_START
		}
		if !isdigit(input) {
			c.parseError = "Invalid http version major, must be digit"
			return BAD
		}
		c.request.VersionMajor = c.request.VersionMajor*10 + int(input) - '0'
		if c.request.VersionMajor > 1 {
			c.parseError = "Unsupported http version"
			return BAD
		}
		return HTTP_VERSION_MAJOR
	case HTTP_VERSION_MINOR_START:
		if !isdigit(input) {
			c.parseError = "Invalid http version minor start, must be digit"
			return BAD
		}
		c.request.VersionMinor = int(input) - '0'
		return HTTP_VERSION_MINOR
	case HTTP_VERSION_MINOR:
		if input == '\r' {
			return STATUS_LINE_LF
		}
		if input == '\n' {
			return FIRST_HEADER_LINE_START
		}
		if is_sp(input) {
			return STATUS_LINE_CR
		}
		if !isdigit(input) {
			c.parseError = "Invalid http version minor, must be digit"
			return BAD
		}
		c.request.VersionMinor = c.request.VersionMinor*10 + int(input) - '0'
		if c.request.VersionMinor > 99 {
			c.parseError = "Invalid http version minor, too big"
			return BAD
		}
		return HTTP_VERSION_MINOR
	case STATUS_LINE_CR:
		if is_sp(input) {
			return STATUS_LINE_CR
		}
		if input != '\n' {
			c.parseError = "Newline is expected"
			return BAD
		}
		return STATUS_LINE_LF
	case STATUS_LINE_LF:
		if input != '\n' {
			c.parseError = "Newline is expected"
			return BAD
		}
		return FIRST_HEADER_LINE_START
	case FIRST_HEADER_LINE_START: // Cannot contain LWS
		c.request.KeepAlive = c.request.VersionMajor == 1 && c.request.VersionMinor >= 1
		if input == '\r' {
			return FINAL_LF
		}
		if input == '\n' {
			return GOOD
		}
		if !is_char(input) || is_ctl(input) || is_tspecial(input) {
			c.parseError = "Invalid character at header line start"
			return BAD
		}
		c.headerKeyStart = incomingReadPos
		incomingBuffer[incomingReadPos] = tolower(input)
		return HEADER_NAME
	case HEADER_LINE_START:
		if is_sp(input) {
			log.Fatalf("Header value continuation is TODO")
			return HEADER_VALUE // value continuation
		}
		if !c.processReadyHeader(incomingBuffer) {
			return BAD
		}
		if input == '\r' {
			return FINAL_LF
		}
		if input == '\n' {
			return GOOD
		}
		if !is_char(input) || is_ctl(input) || is_tspecial(input) {
			c.parseError = "Invalid character at header line start"
			return BAD
		}
		c.headerKeyStart = incomingReadPos
		incomingBuffer[incomingReadPos] = tolower(input)
		return HEADER_NAME
	case HEADER_NAME:
		// We relax https://tools.ietf.org/html/rfc7230#section-3.2.4
		if is_sp(input) {
			c.headerKeyFinish = incomingReadPos
			return HEADER_COLON
		}
		if input != ':' {
			if !is_char(input) || is_ctl(input) || is_tspecial(input) {
				c.parseError = "Invalid character at header name"
				return BAD
			}
			incomingBuffer[incomingReadPos] = tolower(input)
			return HEADER_NAME
		}
		c.headerKeyFinish = incomingReadPos
		fallthrough
	case HEADER_COLON:
		if is_sp(input) {
			return HEADER_COLON
		}
		if input != ':' {
			c.parseError = "':' expected"
			return BAD
		}
		// We will add other comma-separated headers if we need them later
		key := incomingBuffer[c.headerKeyStart:c.headerKeyFinish]
		c.headerCMSList = bytes.Compare(key, strConnection) == 0 || bytes.Compare(key, strTransferEncoding) == 0
		return SPACE_BEFORE_HEADER_VALUE
	case SPACE_BEFORE_HEADER_VALUE:
		if is_sp(input) {
			return SPACE_BEFORE_HEADER_VALUE
		}
		c.headerValueStart = incomingReadPos
		fallthrough
	case HEADER_VALUE:
		if input == '\r' {
			c.headerValueFinish = incomingReadPos
			return HEADER_LF
		}
		if input == '\n' {
			c.headerValueFinish = incomingReadPos
			return HEADER_LINE_START
		}
		if is_ctl(input) {
			c.parseError = "Invalid character (control) in header value"
			return BAD
		}
		if c.headerCMSList && input == ',' {
			log.Fatalf("CMS list collapsing is not supported yet")
			//c.processReadyHeader();
			//header.value.clear();
			return SPACE_BEFORE_HEADER_VALUE
		}
		return HEADER_VALUE
	case HEADER_LF:
		if input != '\n' {
			c.parseError = "Expecting newline"
			return BAD
		}
		return HEADER_LINE_START
	case FINAL_LF:
		if input != '\n' {
			c.parseError = "Expecting final newline"
			return BAD
		}
		return GOOD
	}
	c.parseError = "Invalid request parser state"
	return BAD
}

var strContentLength = []byte("content-length")
var strChunked = []byte("chunked")
var strIdentity = []byte("identity")
var strHost = []byte("host")
var strOrigin = []byte("origin")
var strContentType = []byte("content-type")
var strClose = []byte("close")
var strKeepAlive = []byte("keep-alive")
var strUpgrade = []byte("upgrade")
var strWebSocket = []byte("websocket")
var strAuthorization = []byte("authorization")

func (c *Client) processReadyHeader(incomingBuffer []byte) bool {
	// We have no backtracking, so cheat here
	for c.headerValueFinish > c.headerValueStart && is_sp(incomingBuffer[c.headerValueFinish-1]) {
		c.headerValueFinish -= 1
	}
	key := incomingBuffer[c.headerKeyStart:c.headerKeyFinish]
	value := incomingBuffer[c.headerValueStart:c.headerValueFinish]
	if c.headerCMSList && len(value) == 0 {
		return true // Empty is NOP in CMS list, like "  ,,keep-alive"
	}
	// Those comparisons are by size first so very fast
	if bytes.Compare(key, strContentLength) == 0 {
		if c.request.ContentLength >= 0 {
			c.parseError = "content length specified more than once"
			return false
		}
		cl, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil || cl < 0 {
			c.parseError = "Content length is not a number"
			return false
		}
		c.request.ContentLength = cl
		return true
	}
	if bytes.Compare(key, strTransferEncoding) == 0 {
		tolowerSlice(value)
		if bytes.Compare(value, strChunked) == 0 {
			if len(c.request.TransferEncodings) != 0 {
				c.parseError = "chunk encoding must be applied last"
				return false
			}
			c.request.TransferEncodingChunked = true
			return true
		}
		if bytes.Compare(value, strIdentity) == 0 {
			return true // like chunked, it is transparent to user
		}
		c.request.TransferEncodings = append(c.request.TransferEncodings, value)
		return true
	}
	if bytes.Compare(key, strHost) == 0 {
		c.request.Host = value
		return true
	}
	if bytes.Compare(key, strOrigin) == 0 {
		c.request.Origin = value
		return true
	}
	if bytes.Compare(key, strContentType) == 0 {
		// TODO parse content type
		//parse_content_type_value(header.value, req.content_type_mime, req.content_type_suffix);
		c.request.ContentType = value
		return true
	}
	if bytes.Compare(key, strConnection) == 0 {
		tolowerSlice(value)
		if bytes.Compare(value, strClose) == 0 {
			c.request.KeepAlive = false
			return true
		}
		if bytes.Compare(value, strKeepAlive) == 0 {
			c.request.KeepAlive = true
			return true
		}
		if bytes.Compare(value, strUpgrade) == 0 {
			c.request.ConnectionUpgrade = true
			return true
		}
		c.parseError = "Invalid 'connection' header value"
		return false
	}
	if bytes.Compare(key, strAuthorization) == 0 {
		// TODO
		//parse_authorization_basic(header.value, req.basic_authorization);
		return true
	}
	if bytes.Compare(key, strUpgrade) == 0 {
		tolowerSlice(value)
		if bytes.Compare(value, strWebSocket) == 0 {
			c.request.UpgradeWebSocket = true
			return true
		}
		c.parseError = "Invalid 'upgrade' header value"
		return false
	}
	/*
		if (bytes.Compare(key, string_view{"sec-websocket-key"}) {
			req.sec_websocket_key = header.value;  // Copy is better here
			return false;
		}
		if (bytes.Compare(key, string_view{"sec-websocket-version"}) {
			req.sec_websocket_version = header.value;  // Copy is better here
			return;
		}*/
	c.request.Headers = append(c.request.Headers, HeaderKV{key: key, value: value})
	return true
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
			n, err := c.conn.Read(incomingBuffer[incomingWritePos:])
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
		wr := c.outgoingWriter
		_, _ = wr.WriteString("HTTP/1.1 200 OK\r\n")
		_, _ = wr.WriteString("date: Thu, 26 Nov 2020 19:32:13 GMT\r\n")
		_, _ = wr.WriteString("server: crab\r\n")
		_, _ = wr.WriteString("content-type: text/plain; charset=utf-8\r\n")
		_, _ = wr.WriteString("content-length: 12\r\n")
		_, _ = wr.WriteString("\r\n")
		_, _ = wr.WriteString("Hello, Crab!\r\n")
		_ = wr.Flush()
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
			conn:           conn,
			incomingBuffer: make([]byte, incomingBufferSize),
			outgoingWriter: bufio.NewWriterSize(conn, outgoingBufferSize),
		}
		go client.routine()
	}
}

var queryKeyBytes = []byte("query")

func requestHandler(ctx *fasthttp.RequestCtx) {
	args := ctx.QueryArgs()

	cond := false
	if queryBytes := args.PeekBytes(queryKeyBytes); queryBytes != nil {
		cond = true
	}
	if cond {
		_, _ = fmt.Fprintf(ctx, "Hello, Cond!")
	} else {
		_, _ = fmt.Fprintf(ctx, "Hello, Crab!")
	}
	ctx.SetContentType("text/plain; charset=utf-8")
}

func fast() {
	if err := fasthttp.ListenAndServe("0.0.0.0:7003", requestHandler); err != nil {
		log.Fatalf("Error in ListenAndServe: %s", err)
	}

}

func slow() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		keys, ok := r.URL.Query()["query"]
		w.Header().Set("content-type", "text/plain; charset=utf-8")

		if !ok || len(keys) < 1 {
			w.Write([]byte("Hello, Cond!"))
			return
		}
		w.Write([]byte("Hello, Crab!"))
	})

	log.Fatal(http.ListenAndServe(":7003", nil))
}

//       Thinkpad  Thinkpad   Macbook Pro
//          2s         20s       5s
// fast:  235335     132181    38148
// slow:  112336     61513     24829
// naive: 320302     180053    43505

func main() {
	s := Server{}
	_ = s.ListerAndServer(":7003")
	return
}
