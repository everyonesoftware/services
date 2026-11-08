package dashboard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"services/internal/operations"
)

func TestAcknowledgeInterruptionRequiresOriginAndPreservesFailedResult(t *testing.T) {
	store, err := operations.Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	o, _ := store.Submit("alpha", "update project", "", "")
	store.Claim()
	store.Finish(o.ID, errors.New("outcome unknown"), true)
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer docker.Close()
	coordinator := &operations.Coordinator{Store: store, Workers: operations.NewEngine(docker.URL, docker.Client())}
	s := New(nil, 1)
	s.SetDurableOperations(store, "services", nil)
	s.SetOperationAcknowledger(coordinator.Acknowledge)
	request := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/api/operations/"+o.ID+"/acknowledge", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := request("https://evil.test"); w.Code != 403 {
		t.Fatalf("cross-origin acknowledgement accepted: %d", w.Code)
	}
	got, _ := store.Get(o.ID)
	if !got.NeedsAttention {
		t.Fatal("cross-origin request cleared gate")
	}
	w := request("http://localhost")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"error"`) || strings.Contains(w.Body.String(), `"needsAttention":true`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	got, _ = store.Get(o.ID)
	if got.NeedsAttention || got.Error != "outcome unknown" {
		t.Fatalf("failed outcome was rewritten: %+v", got)
	}
}

func TestAcknowledgeCannotBypassUnavailableDurableConfiguration(t *testing.T) {
	s := New(nil, 1)
	s.SetDurableOperations(nil, "services", errors.New("invalid mounts"))
	r := httptest.NewRequest("POST", "http://localhost/api/operations/compose-dead/acknowledge", nil)
	r.Header.Set("Origin", "http://localhost")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
