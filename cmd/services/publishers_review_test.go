package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"services/internal/auth"
	"services/internal/dashboard"
	"services/internal/publishers"
	"services/internal/updates"
)

func TestPublisherRestartStartupFailureFencesSavedManagedKeys(t *testing.T) {
	for _, failure := range []string{"open", "configure"} {
		t.Run(failure, func(t *testing.T) {
			directory := t.TempDir()
			keys, err := auth.Open(filepath.Join(directory, "auth.db"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = keys.Bootstrap("admin", "a-test-password-with-enough-characters"); err != nil {
				t.Fatal(err)
			}
			key, token, err := keys.CreateKey("saved publisher", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
			if err != nil {
				t.Fatal(err)
			}
			old, err := publishers.Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			if err = old.Save(publishers.Snapshot{Connections: []publishers.Connection{{ID: "saved-connection", KeyID: key.ID, Secret: token, ImageRepository: "ghcr.io/acme/app", Repository: publishers.Repository{FullName: "acme/app"}}}}); err != nil {
				t.Fatal(err)
			}
			if err = old.Close(); err != nil {
				t.Fatal(err)
			}
			if err = keys.Close(); err != nil {
				t.Fatal(err)
			}
			keys, err = auth.Open(filepath.Join(directory, "auth.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer keys.Close()
			origin := "https://services.example"
			if failure == "configure" {
				origin = "http://services.example"
			}
			handler, err := dashboard.NewWithActionsAndPublicOrigin(publicationConfigSource{}, nil, nil, 1, nil, origin)
			if err != nil {
				t.Fatal(err)
			}
			if err = handler.ConfigureAuthentication(keys, "a-local-first-admin-token-with-at-least-32-bytes"); err != nil {
				t.Fatal(err)
			}
			publications, err := configurePublicationsAt(handler, updates.NewChecker(nil, 0), directory, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer publications.Close()
			if failure == "open" {
				if err = os.WriteFile(filepath.Join(directory, "publishers.key"), []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			store, err := configurePublishersAt(handler, directory)
			if err == nil {
				if store != nil {
					store.Close()
				}
				t.Fatal("expected startup failure")
			}
			body := `{"schemaVersion":1,"repository":"ghcr.io/acme/app","tags":["latest"],"digest":"sha256:` + strings.Repeat("a", 64) + `","sourceRepository":"attacker/app","commit":"` + strings.Repeat("b", 40) + `","ref":"refs/heads/main","runId":"123","runAttempt":1,"runUrl":"https://github.com/attacker/app/actions/runs/123","publishedAt":"2026-11-09T12:00:00Z"}`
			r := httptest.NewRequest("POST", origin+"/api/images/published", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 503 {
				t.Fatalf("startup %s bypassed managed source binding: %d %s", failure, w.Code, w.Body.String())
			}
		})
	}
}
