package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/valyala/fasthttp"
)

type Server struct {
	listener net.Listener
}

type Client struct {
	conn net.Conn
	incomingBuffer []byte
	incomingReadPos int
	incomingWritePos int

	outgoingWriter *bufio.Writer
}

const incomingBufferSize = 4096
const outgoingBufferSize = 4096
const maxHeaderSize = 2048

const NaiveParserHeaderText = 1
const NaiveParserHeaderStartLF = 2
const NaiveParserHeaderStart = 3
const NaiveParserSecondLF = 4
const NaiveParserGood = 5

func consume(input byte, state int) (int, error) {
	switch state {
	case NaiveParserHeaderText:
		if input == '\r' {
			return NaiveParserHeaderStartLF, nil
		}
		if input == '\n' {
			return NaiveParserHeaderStart, nil
		}
		return NaiveParserHeaderText, nil
	case NaiveParserHeaderStartLF:
		if input != '\n' {
			return NaiveParserGood, fmt.Errorf("Invalid LF at method start")
		}
		return NaiveParserHeaderStart, nil
	case NaiveParserHeaderStart:
		if input == '\r' {
			return NaiveParserSecondLF, nil
		}
		if input == '\n' {
			return NaiveParserGood, nil
		}
		return NaiveParserHeaderText, nil
	case NaiveParserSecondLF:
		if input != '\n' {
			return NaiveParserGood, fmt.Errorf("Invalid LF at method start")
		}
		return NaiveParserGood, nil
	}
	return NaiveParserGood, fmt.Errorf("Invalid request parser state")
}

func (c *Client) readRequest() error {
	var err error
	state := NaiveParserHeaderText
	incomingBuffer := c.incomingBuffer
	incomingReadPos := c.incomingReadPos
	incomingWritePos := c.incomingWritePos
	if incomingReadPos == incomingWritePos {
		// If possible, start reading from the buffer beginning
		incomingReadPos = 0
		incomingWritePos = 0
	}
	if incomingReadPos + maxHeaderSize > len(incomingBuffer) {
		// Inplace fragments cannot be circular, if in doubt, defragment
		incomingWritePos = copy(incomingBuffer[incomingReadPos:incomingWritePos], incomingBuffer)
		incomingReadPos = 0
	}
	headerLimit := incomingReadPos + maxHeaderSize // Always <= len(incomingBuffer)

	for state != NaiveParserGood {
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
		state, err = consume(incomingBuffer[incomingReadPos], state)
		if err != nil {
			return err
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
			conn:conn,
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
