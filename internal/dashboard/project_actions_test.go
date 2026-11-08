package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"services/internal/groups"
)

type projectUpdaterStub struct {
	fakeServiceUpdater
	members []groups.Container
	run     func(context.Context) error
}

func (f *projectUpdaterStub) UpdateProject(ctx context.Context, members []groups.Container) error {
	f.members = append([]groups.Container(nil), members...)
	if f.run != nil {
		return f.run(ctx)
	}
	return nil
}
func projectRequest(name string) *http.Request {
	r := httptest.NewRequest("POST", "http://dashboard.test/api/projects/"+name+"/update", nil)
	r.Header.Set("Origin", "http://dashboard.test")
	return r
}
func projectTestServer(containers []groups.Container, updater *projectUpdaterStub) *Server {
	s := NewWithActionsAndAllowedHosts(fakeSource{containers: containers}, nil, updater, 42, []string{"dashboard.test"})
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("POST /api/projects/{name}/update", s.projectUpdate)
	return s
}
func projectEvents(t *testing.T, response *httptest.ResponseRecorder) []map[string]string {
	t.Helper()
	var events []map[string]string
	for _, line := range strings.Split(strings.TrimSpace(response.Body.String()), "\n") {
		var event map[string]string
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}
func TestProjectUpdateRejectsUntrustedOriginsAndHosts(t *testing.T) {
	for _, tc := range []struct{ name, host, origin string }{
		{"cross origin", "dashboard.test", "http://evil.test"},
		{"untrusted host", "evil.test", "http://evil.test"},
		{"missing origin", "dashboard.test", ""},
		{"wrong scheme", "dashboard.test", "https://dashboard.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updater := &projectUpdaterStub{}
			s := projectTestServer([]groups.Container{{Project: "shop", Service: "web", UpdateAvailable: true}}, updater)
			r := projectRequest("shop")
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 403 || len(updater.members) != 0 {
				t.Fatalf("code=%d members=%v", w.Code, updater.members)
			}
		})
	}
}

func TestProjectUpdateRejectsMissingOrUnchangedProjects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		members []groups.Container
		code    int
	}{
		{"unknown", nil, 404},
		{"shop", []groups.Container{{Project: "shop", Service: "web"}}, 409},
		{"standalone", []groups.Container{{Service: "web", UpdateAvailable: true}}, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updater := &projectUpdaterStub{}
			s := projectTestServer(tc.members, updater)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, projectRequest(tc.name))
			if w.Code != tc.code || len(updater.members) != 0 {
				t.Fatalf("code=%d members=%v", w.Code, updater.members)
			}
		})
	}
}
func TestProjectUpdateUsesFreshEngineInventory(t *testing.T) {
	updater := &projectUpdaterStub{}
	s := projectTestServer([]groups.Container{{Project: "shop", Service: "web", UpdateAvailable: true}}, updater)
	s.SetActionSource(fakeSource{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, projectRequest("shop"))
	if w.Code != 404 || len(updater.members) != 0 {
		t.Fatalf("stale inventory used: code=%d members=%v", w.Code, updater.members)
	}
}

func TestProjectUpdateSerializesGloballyUntilDetachedWorkCompletes(t *testing.T) {
	started := make(chan context.Context, 1)
	finish := make(chan struct{})
	done := make(chan struct{})
	updater := &projectUpdaterStub{run: func(ctx context.Context) error { started <- ctx; <-finish; return nil }}
	s := projectTestServer([]groups.Container{{Project: "shop", Service: "web", UpdateAvailable: true}}, updater)
	r := projectRequest("shop")
	requestCtx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(requestCtx)
	response := httptest.NewRecorder()
	go func() { defer close(done); s.ServeHTTP(response, r) }()
	ctx := <-started
	cancel()
	defer func() { close(finish); <-done }()
	if ctx.Err() != nil {
		t.Fatal("browser cancellation stopped Compose work")
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 15*time.Minute {
		t.Fatal("missing bounded background timeout")
	}
	otherUpdater := &projectUpdaterStub{}
	other := projectTestServer([]groups.Container{{Project: "other", Service: "web", UpdateAvailable: true}}, otherUpdater)
	w := httptest.NewRecorder()
	other.ServeHTTP(w, projectRequest("other"))
	if w.Code != 409 || len(otherUpdater.members) != 0 {
		t.Fatalf("overlapping mutation admitted: code=%d members=%v", w.Code, otherUpdater.members)
	}
}

func TestProjectUpdateRejectsSelfProject(t *testing.T) {
	for _, tc := range []struct {
		config, project string
		code            int
	}{
		{"", "services", 409}, {"control-plane", "control-plane", 409}, {"INVALID", "shop", 503},
	} {
		t.Run(tc.config+tc.project, func(t *testing.T) {
			t.Setenv("SERVICES_PROJECT_NAME", tc.config)
			updater := &projectUpdaterStub{}
			s := projectTestServer([]groups.Container{{Project: tc.project, Service: "web", UpdateAvailable: true}}, updater)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, projectRequest(tc.project))
			if w.Code != tc.code || len(updater.members) != 0 {
				t.Fatalf("code=%d members=%v", w.Code, updater.members)
			}
		})
	}
}

func TestProjectUpdateFailureEmitsTerminalErrorAndReleasesGate(t *testing.T) {
	updater := &projectUpdaterStub{run: func(context.Context) error { return errors.New("pull failed") }}
	s := projectTestServer([]groups.Container{{Project: "shop", Service: "web", UpdateAvailable: true}}, updater)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, projectRequest("shop"))
	events := projectEvents(t, w)
	if events[len(events)-1]["type"] != "error" || events[len(events)-1]["message"] != "pull failed" {
		t.Fatalf("events = %v", events)
	}
	updater.run = nil
	w = httptest.NewRecorder()
	s.ServeHTTP(w, projectRequest("shop"))
	events = projectEvents(t, w)
	if w.Code != 200 || events[len(events)-1]["type"] != "complete" {
		t.Fatalf("gate not released: code=%d events=%v", w.Code, events)
	}
}

func TestProjectUpdateAppliesRegistryStatusBeforeSelection(t *testing.T) {
	updater := &projectUpdaterStub{}
	s := projectTestServer([]groups.Container{{Project: "shop", Service: "web"}}, updater)
	s.updates = fakeUpdateApplier{}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, projectRequest("shop"))
	if w.Code != 200 || len(updater.members) != 1 || !updater.members[0].UpdateAvailable {
		t.Fatalf("code=%d members=%v", w.Code, updater.members)
	}
}

func TestProjectUpdateStreamsExplicitCompletion(t *testing.T) {
	updater := &projectUpdaterStub{}
	members := []groups.Container{
		{ID: "web1", Project: "shop", Service: "web", UpdateAvailable: true},
		{ID: "web2", Project: "shop", Service: "web", UpdateAvailable: true},
		{ID: "db1", Project: "shop", Service: "db"},
		{ID: "other", Project: "other", Service: "api", UpdateAvailable: true},
	}
	s := projectTestServer(members, updater)
	response := httptest.NewRecorder()
	s.ServeHTTP(response, projectRequest("shop"))
	if response.Code != 200 || len(updater.members) != 3 {
		t.Fatalf("status=%d members=%v body=%s", response.Code, updater.members, response.Body.String())
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatal("want NDJSON")
	}
	events := projectEvents(t, response)
	if len(events) < 2 || events[0]["type"] != "progress" || events[len(events)-1]["type"] != "complete" {
		t.Fatalf("events=%v", events)
	}
}
