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

func (p *Server) parse(incomingBuffer []byte, conn net.Conn) error {
	var err error
	state := NaiveParserHeaderText
	incomingReadPos := 0
	incomingWritePos := 0

	for state != NaiveParserGood {
		if incomingReadPos == incomingWritePos {
			if incomingWritePos == len(incomingBuffer) {
				return fmt.Errorf("Too big request headers")
			}
			n, err := conn.Read(incomingBuffer[incomingWritePos:])
			if err != nil {
				return err
			}
			if n == 0 {
				continue // TODO why
			}
			incomingWritePos += n
		}
		state, err = consume(incomingBuffer[incomingReadPos], state)
		if err != nil {
			return err
		}
		incomingReadPos++
	}
	return nil
}

func (s *Server) routine(conn net.Conn) {
	const bufSize = 4096
	incomingBuffer := make([]byte, bufSize)

	wr := bufio.NewWriterSize(conn, bufSize)
	for {
		err := s.parse(incomingBuffer, conn)
		if err != nil {
			return
		}
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
		go s.routine(conn)
	}
}

var queryKeyBytes = []byte("query") // SQL query in form 'SELECT ... FROM ...'

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

//          2s         20s
// fast:  235335     132181
// slow:  112336     61513
// naive: 320302     180053

func main() {
	s := Server{}
	_ = s.ListerAndServer(":7003")
	return
}
