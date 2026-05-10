package main

import (
	"log"
	"net/http"
	"os"

	"heartbeat-watch-go/watch"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8801"
	}
	server := watch.NewServer(nil)
	log.Printf("heartbeat-watch-go listening on %s", port)
	if err := http.ListenAndServe("0.0.0.0:"+port, server.Handler()); err != nil {
		log.Fatal(err)
	}
}
