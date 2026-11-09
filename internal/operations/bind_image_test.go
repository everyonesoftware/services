package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"services/internal/groups"
)

func TestBindSelfUpdateRequiresTargetStorageCapability(t *testing.T) {
	for _, tc := range []struct {
		name, storage, capability string
		allowed                   bool
	}{
		{"named volume permits existing worker protocol", "volume", "", true},
		{"bind rejects old image", "bind", "", false},
		{"bind accepts compatible image", "bind", "1", true},
		{"bind rejects unknown capability", "bind", "2", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, err := Open(filepath.Join(t.TempDir(), "operations.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			operation, _ := store.Submit("services", "update project", "", "")
			store.Claim()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				labels := map[string]string{"io.services.worker.protocol": "1"}
				if tc.capability != "" {
					labels["io.services.storage.bind"] = tc.capability
				}
				json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:expected", "Config": map[string]any{"Labels": labels}})
			}))
			defer server.Close()
			source := &fixtureSource{members: []groups.Container{{ID: "old", Name: "services", Project: "services", Service: "services", State: "running", Image: "fixture/services:latest"}}}
			updater := &imageGateUpdater{}
			executor := Executor{Store: store, Source: source, Checker: &fixtureChecker{}, Updater: updater, Engine: NewEngine(server.URL, server.Client()), Project: "services", Service: "services", Storage: StorageMount{Type: tc.storage}}
			err = executor.Execute(context.Background(), operation.ID)
			if updater.validated != tc.allowed {
				t.Fatalf("target image gate=%v want %v; error=%v", updater.validated, tc.allowed, err)
			}
			if !tc.allowed && (err == nil || !strings.Contains(err.Error(), "host-folder")) {
				t.Fatalf("missing actionable pre-apply rejection: %v", err)
			}
			got, _ := store.Get(operation.ID)
			if got.NeedsAttention {
				t.Fatal("pre-apply compatibility rejection must not pause the queue as an uncertain mutation")
			}
		})
	}
}
