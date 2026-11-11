package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"services/internal/groups"
	"services/internal/monitor"
)

type resolutionSource struct {
	containers []groups.Container
	err        error
	reads      int
}

func (s *resolutionSource) Containers() ([]groups.Container, error) {
	s.reads++
	return s.containers, s.err
}

func resolutionServer(t *testing.T) (*Server, *monitor.Monitor, *resolutionSource) {
	t.Helper()
	old := groups.Container{ID: "old-id", Project: "agent", Service: "everyoneagent", Name: "everyoneagent", Image: "ghcr.io/example/agent:latest", State: "running", Health: "healthy"}
	source := &resolutionSource{containers: []groups.Container{old}}
	m := monitor.New(source, nil)
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	source.containers = []groups.Container{{ID: "new-id", Project: "agent", Service: "agent", Name: "agent", Image: old.Image, State: "running", Health: "healthy"}}
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	return New(m, 1), m, source
}
func resolutionRequest(s *Server, path, body, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://localhost"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func resolutionBody(t *testing.T, m *monitor.Monitor, action string) string {
	t.Helper()
	incidents := m.Snapshot().Incidents
	if len(incidents) != 1 {
		t.Fatalf("wanted one missing incident: %+v", incidents)
	}
	decision := map[string]any{"incidentId": incidents[0].ID, "since": incidents[0].Since, "action": action}
	if action == "replace" {
		decision["replacementId"] = "new-id"
	}
	if action == "snooze" {
		decision["snoozeSeconds"] = 3600
	}
	body, err := json.Marshal(map[string]any{"decisions": []any{decision}})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func TestResolutionRoutesApplyReadBackAndUndoWithoutDockerMutation(t *testing.T) {
	s, m, source := resolutionServer(t)
	before := source.reads
	w := resolutionRequest(s, "/api/monitor/resolve", resolutionBody(t, m, "replace"), "http://localhost")
	if w.Code != 200 {
		t.Fatalf("resolve=%d %s", w.Code, w.Body.String())
	}
	var receipt struct {
		UndoToken string `json:"undoToken"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.UndoToken == "" {
		t.Fatalf("invalid receipt: %s %v", w.Body.String(), err)
	}
	if source.reads <= before {
		t.Fatal("resolution did not validate a fresh engine snapshot")
	}
	if len(m.Snapshot().Incidents) != 0 {
		t.Fatal("missing incident not cleared")
	}
	projects := httptest.NewRecorder()
	s.ServeHTTP(projects, httptest.NewRequest("GET", "/api/projects", nil))
	if projects.Code != 200 || strings.Contains(projects.Body.String(), "everyoneagent") || !strings.Contains(projects.Body.String(), `"name":"agent"`) {
		t.Fatalf("project readback: %d %s", projects.Code, projects.Body.String())
	}
	if len(source.containers) != 1 || source.containers[0].ID != "new-id" {
		t.Fatal("engine inventory changed")
	}
	undoJSON, _ := json.Marshal(map[string]string{"token": receipt.UndoToken})
	w = resolutionRequest(s, "/api/monitor/undo", string(undoJSON), "http://localhost")
	if w.Code != 204 || len(m.Snapshot().Incidents) != 1 {
		t.Fatalf("undo=%d %s snapshot=%+v", w.Code, w.Body.String(), m.Snapshot())
	}
	w = resolutionRequest(s, "/api/monitor/undo", string(undoJSON), "http://localhost")
	if w.Code != 409 {
		t.Fatalf("replayed undo=%d %s", w.Code, w.Body.String())
	}
}
func TestResolutionRoutesRejectCrossOriginAndMalformedBodies(t *testing.T) {
	for _, path := range []string{"/api/monitor/resolve", "/api/monitor/undo"} {
		t.Run(path, func(t *testing.T) {
			s, m, _ := resolutionServer(t)
			w := resolutionRequest(s, path, `{}`, "https://evil.example")
			if w.Code != 403 {
				t.Fatalf("cross-origin=%d %s", w.Code, w.Body.String())
			}
			for _, body := range []string{`{}`, `null`, `[]`, `{"unknown":true}`, `{} {}`, `{`, strings.Repeat(" ", 70*1024) + `{}`} {
				w = resolutionRequest(s, path, body, "http://localhost")
				if w.Code != 400 && w.Code != 413 {
					t.Fatalf("body %.50q status=%d %s", body, w.Code, w.Body.String())
				}
			}
			if len(m.Snapshot().Incidents) != 1 {
				t.Fatal("bad request changed state")
			}
		})
	}
}
func TestResolutionRoutesFailClosedWithoutMonitor(t *testing.T) {
	s := New(fakeSource{}, 1)
	for _, tc := range []struct{ path, body string }{
		{"/api/monitor/resolve", `{"decisions":[{"incidentId":"a","since":"now","action":"forget"}]}`},
		{"/api/monitor/undo", `{"token":"unknown"}`},
	} {
		w := resolutionRequest(s, tc.path, tc.body, "http://localhost")
		if w.Code != 503 {
			t.Fatalf("%s=%d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
func TestResolutionRoutesMapConflictInvalidAndUnavailable(t *testing.T) {
	s, m, source := resolutionServer(t)
	valid := resolutionBody(t, m, "replace")
	for _, tc := range []struct {
		body   string
		status int
	}{
		{strings.Replace(valid, `"replace"`, `"invalid"`, 1), 400},
		{strings.Replace(valid, `"new-id"`, `"missing-target"`, 1), 409},
		{strings.Replace(valid, m.Snapshot().Incidents[0].Since, "2000-01-01T00:00:00Z", 1), 409},
	} {
		w := resolutionRequest(s, "/api/monitor/resolve", tc.body, "http://localhost")
		if w.Code != tc.status {
			t.Fatalf("want %d got %d %s", tc.status, w.Code, w.Body.String())
		}
		if len(m.Snapshot().Incidents) != 1 {
			t.Fatal("rejected decision changed state")
		}
	}
	source.err = errors.New("https://user:SECRET@engine.example")
	w := resolutionRequest(s, "/api/monitor/resolve", valid, "http://localhost")
	if w.Code != 503 || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatalf("outage response=%d %s", w.Code, w.Body.String())
	}
}
func TestResolutionRoutesRequireAdminSessionNotScopedKeys(t *testing.T) {
	s, store := securedServer(t)
	for _, scope := range []string{"services:read", "publications:write"} {
		var repositories []string
		if scope == "publications:write" {
			repositories = []string{"ghcr.io/example/agent"}
		}
		_, token, err := store.CreateKey("test", []string{scope}, repositories, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/monitor/resolve", "/api/monitor/undo"} {
			w := authRequest(s, "POST", path, map[string]any{}, nil, token)
			if w.Code != 403 {
				t.Fatalf("%s %s=%d", scope, path, w.Code)
			}
		}
	}
	for _, path := range []string{"/api/monitor/resolve", "/api/monitor/undo"} {
		w := authRequest(s, "POST", path, map[string]any{}, nil, "")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s=%d", path, w.Code)
		}
	}
}
