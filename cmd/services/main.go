package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"services/internal/composeupdate"
	"services/internal/dashboard"
	"services/internal/dockerapi"
	"services/internal/monitor"
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
	id, worker, modeErr := parseMode(os.Args[1:])
	if modeErr != nil {
		log.Fatal(modeErr)
	}
	if worker {
		if err := runWorker(id, socket); err != nil {
			log.Fatal(err)
		}
		return
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	source := dockerapi.NewUnixSocketClient(socket)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	watcher := monitor.New(source, monitor.NewWebhook(os.Getenv("SERVICES_WEBHOOK_URL")))
	go watcher.Run(ctx)
	checker := updates.NewChecker(updates.NewRegistryClient(nil), 0)
	composeUpdater := composeupdate.New(
		os.Getenv("SERVICES_COMPOSE_HOST_ROOT"),
		os.Getenv("SERVICES_COMPOSE_MOUNT_ROOT"),
		socket,
	)
	allowedHosts := strings.Split(os.Getenv("SERVICES_ALLOWED_HOSTS"), ",")
	handler, err := dashboard.NewWithActionsAndPublicOrigin(watcher, checker, composeUpdater, version, allowedHosts, os.Getenv("SERVICES_PUBLIC_ORIGIN"))
	if err != nil {
		log.Fatalf("invalid SERVICES_PUBLIC_ORIGIN: %v", err)
	}
	handler.SetActionSource(source)
	if err := configureIcons(handler); err != nil {
		log.Printf("Custom icon editing disabled: %v", err)
	}
	publications, err := configurePublications(handler, checker)
	if err != nil {
		log.Printf("Publication notifications disabled: %v", err)
	} else if publications != nil {
		defer func() {
			stop()
			_ = publications.Close()
		}()
	}
	configureOperations(ctx, handler, socket)
	go checker.Run(ctx, source)
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Services listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
