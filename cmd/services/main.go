package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"services/internal/dashboard"
	"services/internal/dockerapi"
)

var appVersion = "0"

func main() {
	version, err := strconv.Atoi(appVersion)
	if err != nil || version < 0 {
		log.Fatalf("invalid build version %q: expected a non-negative integer", appVersion)
	}
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
		Handler:           dashboard.New(dockerapi.NewUnixSocketClient(socket), version),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Services listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}
