package monitor

import (
	"context"
	"errors"
	"services/internal/groups"
	"testing"
)

func TestMissingContainersRemainInGroupHealth(t *testing.T) {
	s := &source{containers: []groups.Container{{Project: "app", Name: "web", State: "running"}}}
	m := New(s, nil)
	m.Poll(context.Background())
	s.containers = nil
	m.Poll(context.Background())
	cached, _ := m.Containers()
	projects := groups.Aggregate(cached)
	if len(projects) != 1 || projects[0].Status != groups.StatusUnhealthy || projects[0].Containers[0].MonitorStatus != "missing" {
		t.Fatalf("missing project vanished from dashboard: %+v", projects)
	}
}
func TestEngineOutageEventsDeduplicateAndRecover(t *testing.T) {
	s := &source{containers: []groups.Container{}}
	m := New(s, nil)
	m.Poll(context.Background())
	s.err = errors.New("SECRET")
	m.Poll(context.Background())
	m.Poll(context.Background())
	if got := m.Snapshot().Events; len(got) != 1 || got[0].Kind != "problem" || got[0].Project != "Docker Engine" {
		t.Fatalf("outage events %+v", got)
	}
	s.err = nil
	m.Poll(context.Background())
	m.Poll(context.Background())
	if got := m.Snapshot().Events; len(got) != 2 || got[1].Kind != "recovery" {
		t.Fatalf("recovery events %+v", got)
	}
}
