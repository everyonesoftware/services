package dashboard

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"services/internal/groups"
	"services/internal/monitor"
	"services/internal/operations"
)

func inventoryFixture(t *testing.T) (*Server, *reconciliationSource, *operations.Store) {
	t.Helper()
	source := &reconciliationSource{container: groups.Container{
		ID: "old", Name: "app-1", Project: "app", Service: "app", State: "running",
	}}
	watcher := monitor.New(source, nil)
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	store, err := operations.Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := New(watcher, 1)
	server.SetDurableOperations(store, "services", nil)
	return server, source, store
}

func finishInventoryOperation(t *testing.T, store *operations.Store, action string, runErr error) {
	t.Helper()
	op, err := store.Submit("app", action, "old", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Change(op.ID, func(o *operations.Operation) { o.State = "running" }); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(op.ID, runErr, false); err != nil {
		t.Fatal(err)
	}
}

func inventoryResponse(server *Server) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/projects", nil))
	return response
}

func TestInventoryCompletionRefreshFailureRetriesWithoutServingStaleActions(t *testing.T) {
	server, source, store := inventoryFixture(t)
	source.replace("replacement", "sha256:installed")
	finishInventoryOperation(t, store, "update project", nil)
	source.err = errors.New("Docker unavailable")
	if response := inventoryResponse(server); response.Code != 503 {
		t.Fatalf("stale snapshot accepted after failed reconciliation: %d %s", response.Code, response.Body.String())
	}
	source.err = nil
	response := inventoryResponse(server)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"replacement"`) {
		t.Fatalf("failed refresh consumed completion instead of retrying: %d %s", response.Code, response.Body.String())
	}
}

func TestInventoryReconcilesEachTerminalOperationAndAfterServerRestart(t *testing.T) {
	for _, action := range []string{"update project", "update", "pull"} {
		for _, failed := range []bool{false, true} {
			t.Run(action+map[bool]string{false: "/complete", true: "/error"}[failed], func(t *testing.T) {
				server, source, store := inventoryFixture(t)
				var runErr error
				if failed {
					runErr = errors.New("verification failed after replacement")
				}
				for _, id := range []string{"replacement", "next-replacement"} {
					source.replace(id, "sha256:installed")
					finishInventoryOperation(t, store, action, runErr)
					response := inventoryResponse(server)
					if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"`+id+`"`) {
						t.Fatalf("terminal operation not reconciled: %d %s", response.Code, response.Body.String())
					}
				}
				// The completion lives in SQLite, not in an in-process callback.
				server = New(server.source, 2)
				server.SetDurableOperations(store, "services", nil)
				source.replace("after-restart", "sha256:installed")
				response := inventoryResponse(server)
				if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"after-restart"`) {
					t.Fatalf("restart lost reconciliation: %d %s", response.Code, response.Body.String())
				}
			})
		}
	}
}

func TestInventoryReconciliationCoalescesConcurrentReaders(t *testing.T) {
	server, source, store := inventoryFixture(t)
	source.replace("replacement", "sha256:installed")
	finishInventoryOperation(t, store, "update project", nil)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := inventoryResponse(server)
			if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"replacement"`) {
				t.Errorf("concurrent reader saw stale inventory: %d %s", response.Code, response.Body.String())
			}
		}()
	}
	wg.Wait()
	if source.reads != 2 {
		t.Fatalf("Docker reads = %d, want initial poll plus one reconciliation", source.reads)
	}
}

func TestInventoryReconciliationSupportsLegacyQueue(t *testing.T) {
	server, source, _ := inventoryFixture(t)
	previous := composeOperations
	composeOperations = &operationQueue{}
	t.Cleanup(func() { composeOperations = previous })
	server.durableProduction = false
	job := composeOperations.beginLegacy("app", "update project")
	source.replace("replacement", "sha256:installed")
	composeOperations.finishLegacy(job, nil)
	response := inventoryResponse(server)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"replacement"`) {
		t.Fatalf("legacy completion not reconciled: %d %s", response.Code, response.Body.String())
	}
}

type heldInventorySource struct {
	source           *reconciliationSource
	mu               sync.Mutex
	started, release chan struct{}
}

func (s *heldInventorySource) Containers() ([]groups.Container, error) {
	items, err := s.source.Containers()
	s.mu.Lock()
	started, release := s.started, s.release
	s.started, s.release = nil, nil
	s.mu.Unlock()
	if started != nil {
		close(started)
		<-release
	}
	return items, err
}

func TestInventoryReconciliationDoesNotReusePollStartedBeforeCompletion(t *testing.T) {
	server, source, store := inventoryFixture(t)
	held := &heldInventorySource{source: source}
	watcher := monitor.New(held, nil)
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.source = watcher
	started, release := make(chan struct{}), make(chan struct{})
	held.started, held.release = started, release
	background := make(chan error, 1)
	go func() { background <- watcher.Poll(context.Background()) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("background poll did not start")
	}
	source.replace("replacement", "sha256:installed")
	finishInventoryOperation(t, store, "update project", nil)
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() { response <- inventoryResponse(server) }()
	close(release)
	if err := <-background; err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-response:
		if got.Code != 200 || !strings.Contains(got.Body.String(), `"id":"replacement"`) {
			t.Fatalf("pre-completion poll blessed stale inventory: %d %s", got.Code, got.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconciliation did not finish")
	}
}
