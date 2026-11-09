package dashboard

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"services/internal/groups"
	"services/internal/operations"
	"services/internal/updates"
)

type publicationRegistry struct{ calls atomic.Int32 }

func (r *publicationRegistry) ManifestDigest(context.Context, string) (string, error) {
	r.calls.Add(1)
	return "sha256:" + strings.Repeat("c", 64), nil
}

func TestPublicationNotificationPersistsAndDisplaysWithoutRegistryOrMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publications.db")
	registry := &publicationRegistry{}
	store, err := updates.OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	checker := updates.NewChecker(registry, 0)
	checker.SetPublicationStore(store)
	source := fakeSource{containers: []groups.Container{{ID: "app-1", Name: "app", Project: "app", Service: "app", Image: "ghcr.io/acme/app:latest", State: "running", RepoDigests: []string{"ghcr.io/acme/app@sha256:" + strings.Repeat("d", 64)}}}}
	updater := &fakeServiceUpdater{}
	server := NewWithActions(source, checker, updater, 1)
	journal, err := operations.Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	server.SetDurableOperations(journal, "services", nil)
	if err = server.ConfigurePublicationNotifications(notificationToken, []string{"ghcr.io/acme/app"}, checker); err != nil {
		t.Fatal(err)
	}
	body := publicationBody(t)
	for _, want := range []string{"accepted", "duplicate"} {
		response := publicationRequest(t, server, body, notificationToken)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"result":"`+want+`"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = updates.OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	checker = updates.NewChecker(registry, 0)
	checker.SetPublicationStore(store)
	server = NewWithActions(source, checker, updater, 1)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "http://localhost/api/projects", nil))
	var data struct {
		Projects []groups.Project `json:"projects"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(data.Projects) != 1 || !data.Projects[0].UpdateAvailable {
		t.Fatalf("status=%d projects=%+v", response.Code, data.Projects)
	}
	container := data.Projects[0].Containers[0]
	if container.UpdatePublication == nil || container.UpdatePublication.Version != "42" || container.ProposedDigest != "sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("publication not hydrated: %+v", container)
	}
	if registry.calls.Load() != 0 || len(updater.pulls) != 0 || len(updater.updates) != 0 {
		t.Fatalf("notification caused registry or mutation: %d %v %v", registry.calls.Load(), updater.pulls, updater.updates)
	}
	queued, err := journal.List()
	if err != nil || len(queued) != 0 {
		t.Fatalf("notification changed operation queue: operations=%v err=%v", queued, err)
	}
}
