package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"services/internal/composeupdate"
	"services/internal/dashboard"
	"services/internal/dockerapi"
	"services/internal/operations"
	"services/internal/updates"
)

var operationID = regexp.MustCompile(`^compose-[0-9a-f]{32}$`)

func parseMode(args []string) (string, bool, error) {
	if len(args) == 0 {
		return "", false, nil
	}
	if len(args) == 3 && args[0] == "worker" && args[1] == "--operation" && operationID.MatchString(args[2]) {
		return args[2], true, nil
	}
	return "", false, errors.New("usage: services [worker --operation compose-<32 lowercase hex digits>]")
}
func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func durableStore() (*operations.Store, error) {
	data := os.Getenv("SERVICES_DATA_DIR")
	if data != "/data" {
		return nil, errors.New("SERVICES_DATA_DIR=/data on writable persistent storage is required")
	}
	return operations.Open(filepath.Join(data, "operations.db"))
}
func configureOperations(ctx context.Context, handler *dashboard.Server, socket string) {
	project := envDefault("SERVICES_PROJECT_NAME", "services")
	service := envDefault("SERVICES_SERVICE_NAME", "services")
	store, err := durableStore()
	if err != nil {
		handler.SetDurableOperations(nil, project, err)
		log.Printf("Compose mutations disabled: %v", err)
		return
	}
	engine := operations.UnixEngine(socket)
	self := os.Getenv("SERVICES_CONTAINER_NAME")
	if self == "" {
		self = envDefault("HOSTNAME", "services")
	}
	discovery, stop := context.WithTimeout(ctx, 15*time.Second)
	config, err := engine.Discover(discovery, self, "/data", os.Getenv("SERVICES_COMPOSE_HOST_ROOT"), os.Getenv("SERVICES_COMPOSE_MOUNT_ROOT"), socket, project, service)
	stop()
	handler.SetDurableOperations(store, project, err)
	if err != nil {
		log.Printf("Compose mutations disabled: %v", err)
		return
	}
	coordinator := &operations.Coordinator{Store: store, Workers: engine, Config: config}
	handler.SetOperationAcknowledger(coordinator.Acknowledge)
	go coordinator.Run(ctx)
}
func runWorker(id, socket string) error {
	ctx, stop := context.WithTimeout(context.Background(), 15*time.Minute)
	defer stop()
	engine := operations.UnixEngine(socket)
	// A helper must prove its managed identity and shared durable mount before it
	// can claim execution. Host CLI invocation is not an alternative executor.
	identity := os.Getenv("HOSTNAME")
	if identity == "" {
		return errors.New("worker must run in its managed Docker helper")
	}
	inspected, err := engine.Inspect(ctx, identity)
	if err != nil {
		return err
	}
	if inspected.Config.Labels["io.services.worker"] != "true" || inspected.Config.Labels["io.services.operation"] != id || inspected.Config.Labels["io.services.schema"] != "1" {
		return errors.New("worker identity or schema does not match operation")
	}
	storage, err := operations.ResolveDataMount(inspected)
	if err != nil {
		return fmt.Errorf("worker durable storage: %w", err)
	}
	store, err := durableStore()
	if err != nil {
		return err
	}
	defer store.Close()
	o, err := store.Get(id)
	if err != nil {
		return err
	}
	if "/"+o.Worker != inspected.Name {
		return fmt.Errorf("operation is assigned to a different worker")
	}
	source := dockerapi.NewUnixSocketClient(socket)
	checker := updates.NewChecker(updates.NewRegistryClient(nil), 0)
	updater := composeupdate.New(os.Getenv("SERVICES_COMPOSE_HOST_ROOT"), os.Getenv("SERVICES_COMPOSE_MOUNT_ROOT"), socket)
	executor := operations.Executor{Store: store, Source: source, Checker: checker, Updater: updater, Engine: engine, Storage: storage, Project: envDefault("SERVICES_PROJECT_NAME", "services"), Service: envDefault("SERVICES_SERVICE_NAME", "services")}
	return executor.Execute(ctx, id)
}
