package main

import (
	"GuRPC/codec"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"
)

func main() {
	addr := make(chan string)
	go startServer(addr)
	conn, _ := net.Dial("tcp", <-addr)
	defer func() {
		_ = conn.Close()
	}()
	time.Sleep(time.Second)
	_ = json.NewEncoder(conn).Encode(codec.DefaultOption)
	c := codec.NewGobCodec(conn)
	for i := range 5 {
		h := &codec.Header{
			ServiceMethod: "fn.Sum",
			Seq:           uint64(i),
		}
		_ = c.Write(h, fmt.Sprintf("GuRPC rq %d", h.Seq))
		_ = c.ReadHeader(h)
		var reply string
		_ = c.ReadBody(&reply)
		log.Println(reply)
	}
}

func startServer(addr chan string) {
	l, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal("network error: ", err)
	}
	log.Println("listening on :8080")
	addr <- l.Addr().String()
	codec.Accept(l)
}
