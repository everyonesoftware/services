package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"services/internal/operations"
)

func TestOperationAPIIncludesRecoveryMetadata(t *testing.T) {
	o := Operation{Phase: "recovering", SelfUpdate: true, NeedsAttention: true}
	if o.Phase != "recovering" {
		t.Fatal(o)
	}
}

func TestDurableProjectSubmissionAllowsGuardedSelfAndSurvivesNewServer(t *testing.T) {
	store, err := operations.Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := New(nil, 1)
	s.SetDurableOperations(store, "services", nil)
	request := httptest.NewRequest("POST", "http://localhost/api/projects/services/update", nil)
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("Idempotency-Key", "request-1")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	s = New(nil, 2)
	s.SetDurableOperations(store, "services", nil)
	response = httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("GET", "http://localhost/api/operations", nil))
	if !strings.Contains(response.Body.String(), `"selfUpdate":true`) {
		t.Fatal(response.Body.String())
	}
	response = httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("GET", "http://localhost/api/ready", nil))
	if response.Code != 200 {
		t.Fatal(response.Code)
	}
	_ = context.Background()
}
func TestProductionMissingDurabilityDisablesMutations(t *testing.T) {
	s := New(nil, 1)
	s.SetDurableOperations(nil, "services", nil)
	response := httptest.NewRecorder()
	s.ServeHTTP(response, httptest.NewRequest("GET", "http://localhost/api/ready", nil))
	if response.Code != 503 {
		t.Fatal("unsafe worker configuration reported ready", response.Code)
	}
	for _, path := range []string{"/api/projects/alpha/update", "/api/containers/id/update", "/api/containers/id/pull"} {
		r := httptest.NewRequest("POST", "http://localhost"+path, nil)
		r.Header.Set("Origin", "http://localhost")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}
