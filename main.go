package main

import (
	"GuRPC/client"
	"GuRPC/codec"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

func main() {
	addr := make(chan string)
	go startServer(addr)
	c, _ := client.Dial("tcp", <-addr)
	defer func() {
		_ = c.Close()
	}()
	time.Sleep(time.Second)
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := fmt.Sprintf("req %d", i)
			var reply string
			if err := c.Call("Foo.Sum", args, &reply); err != nil {
				log.Fatal(err)
			}
			log.Println(reply)
		}(i)
	}
	wg.Wait()
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
