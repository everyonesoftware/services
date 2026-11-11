package dashboard

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"services/internal/groups"
	"services/internal/monitor"
	"services/internal/operations"
	"services/internal/updates"
)

type reconciliationSource struct {
	mu        sync.Mutex
	container groups.Container
	reads     int
	err       error
}

func (s *reconciliationSource) Containers() ([]groups.Container, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	return []groups.Container{s.container}, s.err
}

func (s *reconciliationSource) replace(id, digest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.container.ID = id
	s.container.RepoDigests = []string{"ghcr.io/acme/app@" + digest}
}

type reconciliationRegistry struct{ digest string }

func (r *reconciliationRegistry) ManifestDigest(context.Context, string) (string, error) {
	return r.digest, nil
}

func TestProjectsReconcileCompletedUpdateBeforeAdvertisingAnotherUpdate(t *testing.T) {
	ctx := context.Background()
	source := &reconciliationSource{container: groups.Container{
		ID: "old", Name: "app-1", Project: "app", Service: "app",
		Image: "ghcr.io/acme/app:latest", State: "running",
		RepoDigests: []string{"ghcr.io/acme/app@sha256:old"},
	}}
	watcher := monitor.New(source, nil)
	if err := watcher.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	registry := &reconciliationRegistry{digest: "sha256:installed"}
	checker := updates.NewChecker(registry, 0)
	if err := checker.CheckNow(ctx, []groups.Container{source.container}); err != nil {
		t.Fatal(err)
	}
	store, err := operations.Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewWithUpdates(watcher, checker, 1)
	server.SetDurableOperations(store, "services", nil)

	readProject := func() groups.Project {
		t.Helper()
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest("GET", "/api/projects", nil))
		if response.Code != 200 {
			t.Fatalf("projects: %d %s", response.Code, response.Body.String())
		}
		var body struct {
			Projects []groups.Project `json:"projects"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Projects) != 1 {
			t.Fatalf("projects: %+v", body.Projects)
		}
		return body.Projects[0]
	}
	if project := readProject(); !project.UpdateAvailable {
		t.Fatal("fixture must initially advertise the update")
	}

	// The worker has verified the replacement, but the periodic monitor has
	// deliberately not polled again. No sleeps or registry propagation involved.
	source.replace("replacement", registry.digest)
	op, err := store.Submit("app", "update project", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Change(op.ID, func(o *operations.Operation) { o.State = "running" }); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(op.ID, nil, false); err != nil {
		t.Fatal(err)
	}

	project := readProject()
	if project.UpdateAvailable || project.Containers[0].ID != "replacement" {
		t.Fatalf("completed update advertised again from stale inventory: %+v", project)
	}
	reads := source.reads
	readProject()
	if source.reads != reads {
		t.Fatal("already reconciled completion must not force Docker reads on every request")
	}

	// A genuinely newer registry digest must remain visible immediately.
	registry.digest = "sha256:newer"
	if err := checker.CheckNow(ctx, []groups.Container{source.container}); err != nil {
		t.Fatal(err)
	}
	project = readProject()
	if !project.UpdateAvailable || project.Containers[0].ProposedDigest != registry.digest {
		t.Fatalf("genuinely newer update was suppressed: %+v", project)
	}
}
