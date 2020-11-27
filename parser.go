package main

import (
	"bytes"
	"strconv"
)

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

func parse_content_type_value(value []byte) ([]byte, []byte) {
	start := bytes.Index(value, []byte{';', ' ', '\t'})
	if start < 0 {
		tolowerSlice(value)
		return value, nil
	}
	mime := value[:start]
	tolowerSlice(mime)
	for start < len(value) && is_sp(value[start]) {
		start++
	}
	if start < len(value) && value[start] == ';' {
		start++ // We simply allow whitespaces instead of ;
	}
	for start < len(value) && is_sp(value[start]) {
		start++
	}
	return mime, value[start:]
}

func parse_authorization_basic(value []byte) []byte {
	if len(value) < 6 || tolower(value[0]) != 'b' || tolower(value[1]) != 'a' ||
		tolower(value[2]) != 's' || tolower(value[3]) != 'i' || tolower(value[4]) != 'c' ||
		!is_sp(value[5]) {
		return nil
	}
	start := 6
	for start < len(value) && is_sp(value[start]) {
		start++
	}
	return value[start:]
}

const (
	METHOD_START              = iota
	METHOD_START_LF           = iota
	METHOD                    = iota
	URI_START                 = iota
	URI                       = iota
	URI_SHIFTED               = iota // After first % characters, we start to copy uri chars to uriWritePos
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
	HEADER_VALUE_CONTINUATION = iota
	HEADER_LF                 = iota
	FINAL_LF                  = iota
	GOOD                      = iota
	BAD                       = iota
)

var strConnection = []byte("connection")
var strTransferEncoding = []byte("transfer-encoding")
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
var strSecWebSocketKey = []byte("sec-websocket-key")
var strSecWebSocketVersion = []byte("sec-websocket-version")

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
		if input == '%' {
			c.uriWritePos = incomingReadPos
			return URI_PERCENT1
		}
		return URI
	case URI:
		if is_sp(input) {
			c.request.Path = incomingBuffer[c.uriStart:incomingReadPos]
			return HTTP_VERSION_H
		}
		if is_ctl(input) {
			c.parseError = "Invalid (control) character in uri"
			return BAD
		}
		if input == '#' {
			c.request.Path = incomingBuffer[c.uriStart:incomingReadPos]
			return URI_ANCHOR
		}
		if input == '?' {
			c.request.Path = incomingBuffer[c.uriStart:incomingReadPos]
			c.queryStringStart = incomingReadPos + 1
			return URI_QUERY_STRING
		}
		if input == '%' {
			c.uriWritePos = incomingReadPos
			return URI_PERCENT1
		}
		return URI
	case URI_SHIFTED:
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
		return URI_SHIFTED
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
			return URI_SHIFTED
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
			c.headerValueWritePos = incomingReadPos - 1
			return HEADER_VALUE_CONTINUATION
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
		c.headerValueWritePos = incomingReadPos
		fallthrough
	case HEADER_VALUE:
		if input == '\r' {
			return HEADER_LF
		}
		if input == '\n' {
			return HEADER_LINE_START
		}
		if is_ctl(input) {
			c.parseError = "Invalid character (control) in header value"
			return BAD
		}
		if c.headerCMSList && input == ',' {
			c.headerValueWritePos = incomingReadPos
			if !c.processReadyHeader(incomingBuffer) {
				return BAD
			}
			c.headerValueStart = incomingReadPos + 1
			return SPACE_BEFORE_HEADER_VALUE
		}
		c.headerValueWritePos += 1
		return HEADER_VALUE
	case HEADER_VALUE_CONTINUATION:
		if input == '\r' {
			return HEADER_LF
		}
		if input == '\n' {
			return HEADER_LINE_START
		}
		if is_ctl(input) {
			c.parseError = "Invalid character (control) in header value"
			return BAD
		}
		if c.headerCMSList && input == ',' {
			if !c.processReadyHeader(incomingBuffer) {
				return BAD
			}
			c.headerValueStart = incomingReadPos + 1
			return SPACE_BEFORE_HEADER_VALUE
		}
		incomingBuffer[c.headerValueWritePos] = input
		c.headerValueWritePos += 1
		return HEADER_VALUE_CONTINUATION
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

func (c *Client) processReadyHeader(incomingBuffer []byte) bool {
	// We have no backtracking, so cheat here
	for c.headerValueWritePos > c.headerValueStart && is_sp(incomingBuffer[c.headerValueWritePos-1]) {
		c.headerValueWritePos -= 1
	}
	key := incomingBuffer[c.headerKeyStart:c.headerKeyFinish]
	value := incomingBuffer[c.headerValueStart:c.headerValueWritePos]
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
		c.request.ContentTypeMime, c.request.ContentTypeSuffix = parse_content_type_value(value)
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
		c.request.basicAuthorization = parse_authorization_basic(value)
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
	if bytes.Compare(key, strSecWebSocketKey) == 0 {
		c.request.SecWebsocketKey = value
		return true
	}
	if bytes.Compare(key, strSecWebSocketVersion) == 0 {
		c.request.SecWebsocketVersion = value
		return true
	}
	c.request.Headers = append(c.request.Headers, HeaderKV{key: key, value: value})
	return true
}
