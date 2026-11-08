package monitor

import (
	"context"
	"services/internal/groups"
	"testing"
	"time"
)

func TestRestartWindowCountsDeltasNotHistoricalTotal(t *testing.T) {
	s := &source{containers: []groups.Container{{ID: "a", Name: "web", State: "running", RestartCount: 40}}}
	m := New(s, nil)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.Poll(context.Background())
	if len(m.Snapshot().Incidents) != 0 {
		t.Fatal("historical count caused alert")
	}
	s.containers[0].RestartCount = 42
	now = now.Add(time.Minute)
	m.Poll(context.Background())
	if len(m.Snapshot().Incidents) != 0 {
		t.Fatal("below threshold")
	}
	s.containers[0].RestartCount = 43
	now = now.Add(time.Minute)
	m.Poll(context.Background())
	if got, _ := m.Containers(); got[0].MonitorStatus != "restart-loop" {
		t.Fatalf("did not detect loop: %+v", got)
	}
	now = now.Add(6 * time.Minute)
	m.Poll(context.Background())
	if len(m.Snapshot().Incidents) != 0 {
		t.Fatal("window did not expire")
	}
	s.containers[0].ID = "new"
	s.containers[0].RestartCount = 0
	m.Poll(context.Background())
	if len(m.Snapshot().Incidents) != 0 {
		t.Fatal("counter reset caused incident")
	}
}
