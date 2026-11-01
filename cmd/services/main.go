package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"services/internal/composeupdate"
	"services/internal/dashboard"
	"services/internal/dockerapi"
	"services/internal/updates"
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
	source := dockerapi.NewUnixSocketClient(socket)
	checker := updates.NewChecker(updates.NewRegistryClient(nil), 0)
	composeUpdater := composeupdate.New(
		os.Getenv("SERVICES_COMPOSE_HOST_ROOT"),
		os.Getenv("SERVICES_COMPOSE_MOUNT_ROOT"),
		socket,
	)
	allowedHosts := strings.Split(os.Getenv("SERVICES_ALLOWED_HOSTS"), ",")
	handler, err := dashboard.NewWithActionsAndPublicOrigin(source, checker, composeUpdater, version, allowedHosts, os.Getenv("SERVICES_PUBLIC_ORIGIN"))
	if err != nil {
		log.Fatalf("invalid SERVICES_PUBLIC_ORIGIN: %v", err)
	}
	go checker.Run(context.Background(), source)
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Services listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}
