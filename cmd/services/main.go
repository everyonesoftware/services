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
	"services/internal/svgicon"
	"services/internal/updates"
)

var appVersion = "0"

func main() {
	// Keep the render child separate from version/configuration, storage, Docker,
	// HTTP listeners and all other application initialization.
	if len(os.Args) == 2 && os.Args[1] == svgicon.WorkerFlag {
		if err := svgicon.RunWorker(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		return
	}
	if handled, err := runAuthCommand(os.Args[1:], os.Getenv("SERVICES_DATA_DIR"), os.Stdin, os.Stdout); handled {
		if err != nil {
			log.Fatal(err)
		}
		return
	}
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
	handler.SetLogSource(source)
	authentication, err := configureAuthenticationAt(handler, os.Getenv("SERVICES_DATA_DIR"))
	if err != nil {
		// Never expose a network-trusted dashboard if authentication cannot start.
		log.Fatalf("Authentication startup failed: %v", err)
	}
	defer authentication.Close()
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
		publisherStore, publisherErr := configurePublishersAt(handler, os.Getenv("SERVICES_DATA_DIR"))
		if publisherErr != nil {
			log.Printf("Publisher setup disabled: %v", publisherErr)
		} else {
			defer publisherStore.Close()
		}
	}
	configureOperations(ctx, handler, socket)
	go checker.Run(ctx, source)
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       90 * time.Second,
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
