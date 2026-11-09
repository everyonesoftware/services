package operations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"services/internal/groups"
	"testing"
)

func TestWorkerUnchangedSelfImageDoesNotRequireProtocolLabels(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	o, err := s.Submit("services", "update project", "", "")
	if err != nil {
		t.Fatal(err)
	}
	s.Claim()
	imageInspects := 0
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/containers/old/json" {
			w.Write([]byte(`{"Id":"old","Image":"sha256:same"}`))
			return
		}
		imageInspects++
		w.Write([]byte(`{"Id":"sha256:same","Config":{"Labels":{}}}`))
	}))
	defer docker.Close()
	updater := &imageGateUpdater{}
	executor := Executor{Store: s, Source: &fixtureSource{members: []groups.Container{{ID: "old", Name: "services", Project: "services", Service: "services", State: "running", Image: "services:latest"}}}, Checker: &fixtureChecker{}, Updater: updater, Engine: NewEngine(docker.URL, docker.Client()), Project: "services", Service: "services"}
	executor.Execute(context.Background(), o.ID)
	if !updater.validated {
		t.Fatal("unchanged immutable Services image rejected for missing protocol labels")
	}
	if imageInspects != 1 {
		t.Fatalf("expected just immutable image lookup, got %d image inspections", imageInspects)
	}
}
