package dashboard

import (
	"net/http/httptest"
	"services/internal/groups"
	"strings"
	"testing"
)

func TestContainerActionCannotBypassSelfUpdateProtection(t *testing.T) {
	s := NewWithActionsAndAllowedHosts(fakeSource{containers: []groups.Container{{ID: "self", Project: "services", Service: "services", State: "running"}}}, nil, &fakeServiceUpdater{}, 0, []string{"dashboard.test"})
	r := httptest.NewRequest("POST", "http://dashboard.test/api/containers/self/update", nil)
	r.Header.Set("Origin", "http://dashboard.test")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("self update accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestContainerActionHonorsGroupActionGate(t *testing.T) {
	s := NewWithActionsAndAllowedHosts(fakeSource{}, nil, &fakeServiceUpdater{}, 0, []string{"dashboard.test"})
	release, ok := s.tryComposeAction("")
	if !ok {
		t.Fatal("gate occupied")
	}
	defer release()
	r := httptest.NewRequest("POST", "http://dashboard.test/api/containers/x/update", nil)
	r.Header.Set("Origin", "http://dashboard.test")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("overlapping action accepted or inventory preflight ran: %d %s", w.Code, w.Body.String())
	}
}

func TestContainerActionsUseFreshEngineInventory(t *testing.T) {
	updater := &fakeServiceUpdater{}
	s := NewWithActionsAndAllowedHosts(fakeSource{containers: []groups.Container{{ID: "old", Project: "media", Service: "web", State: "running"}}}, nil, updater, 0, []string{"dashboard.test"})
	s.SetActionSource(fakeSource{})
	r := httptest.NewRequest("POST", "http://dashboard.test/api/containers/old/update", nil)
	r.Header.Set("Origin", "http://dashboard.test")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 404 || len(updater.updates) != 0 {
		t.Fatalf("stale container used: code %d body %s", w.Code, w.Body.String())
	}
}
func TestContainerStreamHasExplicitTerminalRecord(t *testing.T) {
	s := NewWithActionsAndAllowedHosts(fakeSource{containers: []groups.Container{{ID: "x", Project: "media", Service: "web", State: "running"}}}, nil, &fakeServiceUpdater{}, 0, []string{"dashboard.test"})
	r := httptest.NewRequest("POST", "http://dashboard.test/api/containers/x/update", nil)
	r.Header.Set("Origin", "http://dashboard.test")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `"type":"complete"`) {
		t.Fatalf("no explicit completion: %s", w.Body.String())
	}
}
