package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/valyala/fasthttp"
)

var queryKeyBytes = []byte("query")
var helloCrab = []byte("Hello, Crab!")

func requestHandler(ctx *fasthttp.RequestCtx) {
	//args := ctx.QueryArgs()

	//cond := false
	//if queryBytes := args.PeekBytes(queryKeyBytes); queryBytes != nil {
	//	cond = true
	//}
	//if cond {
	//	_, _ = fmt.Fprintf(ctx, "Hello, Cond!")
	//} else {
	_, _ = ctx.Write(helloCrab)
	//_, _ = fmt.Fprintf(ctx, "Hello, Crab!")
	//}
	ctx.SetContentType("text/plain; charset=utf-8")
}

func fast() {
	if err := fasthttp.ListenAndServe(":7002", requestHandler); err != nil {
		log.Fatalf("Error in ListenAndServe: %s", err)
	}
}

func slow() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		//keys, ok := r.URL.Query()["query"]
		w.Header().Set("content-type", "text/plain; charset=utf-8")

		//if !ok || len(keys) < 1 {
		//	w.Write([]byte("Hello, Cond!"))
		//	return
		//}
		_, _ = w.Write(helloCrab)
	})

	log.Fatal(http.ListenAndServe(":7001", nil))
}

// gobench -t 20 -c 128 -u http://127.0.0.1:7001/index.html
// gobench -t 20 -c 128 -u http://127.0.0.1:7002/index.html
// gobench -t 20 -c 128 -u http://127.0.0.1:7003/index.html

//            Thinkpad  Thinkpad   Macbook Pro
//              2s         20s       5s
// net.http:  112336     61513     24829
// fasthttp:  235335     132181    43612
// schwidko:     ?         ?       44699

func main() {
	fmt.Println("Runs 3 web servers")
	fmt.Println(" net.http: port 7001")
	fmt.Println(" fasthttp: port 7002")
	fmt.Println(" schwidko: port 7003")

	s := Server{handler: func(wr ResponseWriter, request *Request) {
		wr.WriteOtherHeader("content-type", "text/plain; charset=utf-8")
		wr.WriteContentLength(12)
		wr.Write(helloCrab)
	}}
	go func() {
		err := s.ListerAndServer(":7003")
		if err != nil {
			log.Fatalf("Cannot run schwidko, %v", err)
		}
	}()
	go fast()
	slow()
}
