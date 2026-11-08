package monitor_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"services/internal/dockerapi"
	"services/internal/groups"
	"services/internal/monitor"
)

func TestDockerEngineInspectionToProjectHealthLifecycle(t *testing.T) {
	var mode atomic.Int32
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.Load() == 3 {
			http.Error(w, "engine offline", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.47"}`)
		case "/v1.47/images/json":
			fmt.Fprint(w, `[{"Id":"sha256:image","Created":1700000000,"RepoDigests":["example/web@sha256:abc"]}]`)
		case "/v1.47/containers/json":
			if r.URL.Query().Get("all") != "1" {
				t.Error("stopped containers must be included")
			}
			if mode.Load() == 2 {
				fmt.Fprint(w, `[]`)
				return
			}
			fmt.Fprint(w, `[{"Id":"web","Names":["/app-web-1"],"State":"running","Image":"example/web:latest","ImageID":"sha256:image","Labels":{"com.docker.compose.project":"app","com.docker.compose.service":"web"}}]`)
		case "/v1.47/containers/web/json":
			health := "healthy"
			if mode.Load() == 1 {
				health = "unhealthy"
			}
			fmt.Fprintf(w, `{"RestartCount":1,"State":{"Status":"running","StartedAt":"2026-11-07T12:00:00Z","ExitCode":0,"OOMKilled":false,"Health":{"Status":%q,"Log":[{"ExitCode":1,"Output":"connection refused"}]}}}`, health)
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	m := monitor.New(dockerapi.New(engine.URL, engine.Client()), nil)
	check := func(status groups.Status, incident string) {
		t.Helper()
		if err := m.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		cached, err := m.Containers()
		if err != nil {
			t.Fatal(err)
		}
		projects := groups.Aggregate(cached)
		if len(projects) != 1 || projects[0].Status != status {
			t.Fatalf("projects %+v", projects)
		}
		snap := m.Snapshot()
		if incident == "" {
			if len(snap.Incidents) != 0 {
				t.Fatalf("unexpected incident %+v", snap)
			}
		} else if len(snap.Incidents) != 1 || snap.Incidents[0].Status != incident {
			t.Fatalf("incidents %+v", snap.Incidents)
		}
	}
	check(groups.StatusHealthy, "")
	mode.Store(1)
	check(groups.StatusUnhealthy, "unhealthy")
	cached, _ := m.Containers()
	if cached[0].HealthOutput != "connection refused" || cached[0].RestartCount != 1 {
		t.Fatalf("inspect fields %+v", cached[0])
	}
	mode.Store(3)
	if err := m.Poll(context.Background()); err == nil {
		t.Fatal("outage not detected")
	}
	if m.Snapshot().Connected || len(m.Snapshot().Incidents) != 1 || m.Snapshot().Incidents[0].Status != "unhealthy" {
		t.Fatalf("stale snapshot %+v", m.Snapshot())
	}
	mode.Store(2)
	check(groups.StatusUnhealthy, "missing")
	mode.Store(0)
	check(groups.StatusHealthy, "")
}
