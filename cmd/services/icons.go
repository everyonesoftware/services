package main

import (
	"errors"
	"os"
	"path/filepath"

	"services/internal/dashboard"
	"services/internal/icons"
)

// Custom artwork is optional. Never fall back to an ephemeral working directory.
func configureIcons(handler *dashboard.Server) error {
	directory := os.Getenv("SERVICES_DATA_DIR")
	if directory == "" || !filepath.IsAbs(directory) {
		return errors.New("custom icons require an absolute SERVICES_DATA_DIR on writable persistent storage")
	}
	store, err := icons.Open(filepath.Join(directory, "icons"))
	if err != nil {
		return err
	}
	handler.SetIconStore(store)
	return nil
}
