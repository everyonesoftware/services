package main

import (
	"path/filepath"
	"services/internal/auth"
	"services/internal/dashboard"
	"services/internal/updates"
	"testing"
)

func TestPublisherStartupRequiresAuthenticatedHTTPSReceiver(t *testing.T) {
	dir := t.TempDir()
	handler := dashboard.New(publicationConfigSource{}, 1)
	if store, err := configurePublishersAt(handler, dir); err == nil || store != nil {
		if store != nil {
			store.Close()
		}
		t.Fatal("publisher setup enabled without auth and HTTPS")
	}
	handler, err := dashboard.NewWithActionsAndPublicOrigin(publicationConfigSource{}, nil, nil, 1, nil, "https://services.example")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := auth.Open(filepath.Join(dir, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer keys.Close()
	if err = handler.ConfigureAuthentication(keys, "a-local-first-admin-token-with-at-least-32-bytes"); err != nil {
		t.Fatal(err)
	}
	publications, err := configurePublicationsAt(handler, updates.NewChecker(nil, 0), dir, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer publications.Close()
	store, err := configurePublishersAt(handler, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
}
