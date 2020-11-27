package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/valyala/fasthttp"
)

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

	helloCrab := []byte("Hello, Crab!")
	s := Server{handler: func(wr ResponseWriter, request *Request) {
		wr.WriteOtherHeader("content-type", "text/plain; charset=utf-8")
		wr.WriteContentLength(12)
		wr.Write(helloCrab)
	}}
	_ = s.ListerAndServer(":7003")
	return
}
