package main

import (
	"demoHTTPProxy/proxy"
	"log"
)

func main() {
	log.Fatalln(proxy.New("127.0.0.1:8080").Run())
}
