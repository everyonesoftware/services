package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"services/internal/dashboard"
	"services/internal/operations"
	"services/internal/updates"
)

// Publication ingestion is optional and never shares credentials with workers.
func configurePublications(handler *dashboard.Server, checker *updates.Checker) (*updates.PublicationStore, error) {
	token := os.Getenv("SERVICES_PUBLISH_TOKEN")
	repositories := os.Getenv("SERVICES_PUBLISH_REPOSITORIES")
	if token == "" && repositories == "" && !handler.AuthenticationConfigured() {
		return nil, nil
	}
	if (token == "") != (repositories == "") {
		return nil, errors.New("both SERVICES_PUBLISH_TOKEN and SERVICES_PUBLISH_REPOSITORIES are required")
	}
	if os.Getenv("SERVICES_DATA_DIR") != "/data" {
		return nil, errors.New("publication notifications require SERVICES_DATA_DIR=/data on persistent writable storage")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	engine := operations.UnixEngine(envDefault("DOCKER_SOCKET", "/var/run/docker.sock"))
	identity := envDefault("SERVICES_CONTAINER_NAME", envDefault("HOSTNAME", "services"))
	inspected, err := engine.Inspect(ctx, identity)
	if err != nil {
		return nil, fmt.Errorf("verify publication storage: %w", err)
	}
	if _, err := operations.ResolveDataMount(inspected); err != nil {
		return nil, fmt.Errorf("verify publication storage: %w", err)
	}
	var allowed []string
	if repositories != "" {
		allowed = strings.Split(repositories, ",")
	}
	for i := range allowed {
		allowed[i] = strings.TrimSpace(allowed[i])
	}
	return configurePublicationsAt(handler, checker, "/data", token, allowed)
}

func configurePublicationsAt(handler *dashboard.Server, checker *updates.Checker, dataDir, token string, repositories []string) (*updates.PublicationStore, error) {
	store, err := updates.OpenPublicationStore(filepath.Join(dataDir, "publications.db"))
	if err != nil {
		return nil, err
	}
	if token == "" && len(repositories) == 0 {
		err = handler.ConfigurePublicationReceiver(checker)
	} else {
		err = handler.ConfigurePublicationNotifications(token, repositories, checker)
	}
	if err != nil {
		store.Close()
		return nil, err
	}
	checker.SetPublicationStore(store)
	return store, nil
}
