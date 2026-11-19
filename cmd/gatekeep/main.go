package main

import (
	"flag"
	"fmt"
	"log"

	"gatekeep/internal/server"
)

func main() {
	port := flag.Int("port", 8765, "listen port")
	flag.Parse()

	if *port < 1 || *port > 65535 {
		log.Fatalf("invalid port %d", *port)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	if err := server.ListenAndServe(addr); err != nil {
		log.Fatal(err)
	}
}
