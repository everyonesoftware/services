package dashboard

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"services/internal/groups"
	"services/internal/operations"
)

func TestDurableLegacyStreamSeparatesAcceptanceFromTerminalResult(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "error"}[failed], func(t *testing.T) {
			store, err := operations.Open(filepath.Join(t.TempDir(), "ops.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			o, _ := store.Submit("services", "update", "target", "key")
			store.Claim()
			s := New(fakeSource{}, 1)
			s.SetDurableOperations(store, "services", nil)
			s.SetActionSource(fakeSource{containers: []groups.Container{{ID: "target", Project: "services", Service: "services", State: "running"}}})
			timer := time.AfterFunc(20*time.Millisecond, func() {
				store.Message(o.ID, "persisted progress")
				var failure error
				if failed {
					failure = errors.New("replacement readiness failed")
				}
				store.Finish(o.ID, failure, false)
			})
			defer timer.Stop()
			r := httptest.NewRequest("POST", "http://localhost/api/containers/target/update", nil)
			r.Header.Set("Origin", "http://localhost")
			r.Header.Set("Idempotency-Key", "key")
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			body := w.Body.String()
			terminal := `"type":"complete"`
			if failed {
				terminal = `"type":"error"`
			}
			if !strings.Contains(body, `"type":"accepted"`) || !strings.Contains(body, terminal) || !strings.Contains(body, "persisted progress") {
				t.Fatal(body)
			}
			if failed && strings.Contains(body, `"type":"complete"`) {
				t.Fatal("failure emitted success")
			}
		})
	}
}
func TestDurableClientDisconnectDoesNotCancelAcceptedWork(t *testing.T) {
	store, _ := operations.Open(filepath.Join(t.TempDir(), "ops.db"))
	defer store.Close()
	s := New(fakeSource{containers: []groups.Container{{ID: "target", Project: "alpha", Service: "web", State: "running"}}}, 1)
	s.SetDurableOperations(store, "services", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "http://localhost/api/containers/target/update", nil).WithContext(ctx)
	r.Header.Set("Origin", "http://localhost")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	ops, err := store.List()
	if err != nil || len(ops) != 1 || ops[0].State != "queued" {
		t.Fatal(ops, err)
	}
	if strings.Contains(w.Body.String(), `"type":"complete"`) {
		t.Fatal("disconnect claimed success")
	}
}
func TestDurableRoutesRetainSameOriginProtection(t *testing.T) {
	store, _ := operations.Open(filepath.Join(t.TempDir(), "ops.db"))
	defer store.Close()
	s := New(nil, 1)
	s.SetDurableOperations(store, "services", nil)
	for _, path := range []string{"/api/projects/services/update", "/api/containers/target/update", "/api/containers/target/pull"} {
		r := httptest.NewRequest("POST", "http://localhost"+path, nil)
		r.Header.Set("Origin", "http://evil.example")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(path, w.Code)
		}
	}
	ops, _ := store.List()
	if len(ops) != 0 {
		t.Fatal("cross-origin work submitted")
	}
}
