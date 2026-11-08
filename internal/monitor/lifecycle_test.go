package monitor

import (
	"context"
	"services/internal/groups"
	"testing"
)

func TestIncidentLifecycleStableIdentityAndDisappearance(t *testing.T) {
	s := &source{containers: []groups.Container{{ID: "a", Project: "app", Service: "web", Name: "web-1", State: "exited", ExitCode: 1}}}
	m := New(s, nil)
	m.Poll(context.Background())
	first := m.Snapshot()
	m.Poll(context.Background())
	second := m.Snapshot()
	if len(second.Events) != 1 || second.Incidents[0].Since != first.Incidents[0].Since {
		t.Fatalf("not deduplicated: %+v", second)
	}
	s.containers[0].ID = "replacement"
	s.containers[0].State = "running"
	m.Poll(context.Background())
	if len(m.Snapshot().Incidents) != 0 || len(m.Snapshot().Events) != 2 || m.Snapshot().Events[1].Kind != "recovery" {
		t.Fatalf("replacement: %+v", m.Snapshot())
	}
	s.containers = nil
	m.Poll(context.Background())
	snap := m.Snapshot()
	if len(snap.Incidents) != 1 || snap.Incidents[0].Status != "missing" {
		t.Fatalf("missing: %+v", snap)
	}
	m.Poll(context.Background())
	if len(m.Snapshot().Events) != 3 {
		t.Fatal("repeated disappearance event")
	}
	s.containers = []groups.Container{{ID: "new", Project: "app", Service: "web", Name: "web-1", State: "running"}}
	m.Poll(context.Background())
	if len(m.Snapshot().Incidents) != 0 {
		t.Fatal("missing did not recover")
	}
}
