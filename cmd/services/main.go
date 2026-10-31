package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"services/internal/dashboard"
	"services/internal/dockerapi"
)

func main() {
	socket := os.Getenv("DOCKER_SOCKET")
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           dashboard.New(dockerapi.NewUnixSocketClient(socket)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Services listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}
