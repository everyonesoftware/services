package main

import (
	"net/http"
	"services/internal/dashboard"
	"services/internal/githubapp"
	"services/internal/publishers"
	"time"
)

// Call only after publication storage has passed its persistent mount check.
func configurePublishersAt(handler *dashboard.Server, directory string) (*publishers.Store, error) {
	handler.BeginPublisherConfiguration()
	store, err := publishers.Open(directory)
	if err != nil {
		return nil, err
	}
	client := githubapp.NewClient(&http.Client{Timeout: 15 * time.Second})
	if err = handler.ConfigurePublishers(store, client); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}
