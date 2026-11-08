package monitor

import (
	"context"
	"errors"
	"services/internal/groups"
	"testing"
)

type source struct {
	containers []groups.Container
	err        error
}

func (s *source) Containers() ([]groups.Container, error) { return s.containers, s.err }
func TestPollClassifiesStateBeforeHealth(t *testing.T) {
	for _, tc := range []struct {
		c         groups.Container
		status    string
		incidents int
	}{
		{groups.Container{State: "running"}, "running", 0},
		{groups.Container{State: "running", Health: "healthy"}, "healthy", 0},
		{groups.Container{State: "running", Health: "unhealthy", HealthOutput: "SECRET"}, "unhealthy", 1},
		{groups.Container{State: "running", Health: "starting"}, "starting", 0},
		{groups.Container{State: "exited", Health: "healthy", ExitCode: 2}, "stopped", 1},
		{groups.Container{State: "paused", Health: "healthy"}, "stopped", 1},
		{groups.Container{State: "created"}, "starting", 0},
		{groups.Container{State: "restarting"}, "stopped", 1},
		{groups.Container{State: "exited", OneShot: true}, "completed", 0},
		{groups.Container{State: "exited", OneShot: true, OOMKilled: true}, "oom-killed", 1},
	} {
		tc.c.Name = "web"
		tc.c.Project = "app"
		tc.c.Service = "web"
		m := New(&source{containers: []groups.Container{tc.c}}, nil)
		if err := m.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		got, err := m.Containers()
		if err != nil {
			t.Fatal(err)
		}
		if got[0].MonitorStatus != tc.status || len(m.Snapshot().Incidents) != tc.incidents {
			t.Errorf("%+v: status %s snapshot %+v", tc.c, got[0].MonitorStatus, m.Snapshot())
		}
	}
}
func TestConnectionFailurePreservesSnapshotAndDoesNotExposeErrors(t *testing.T) {
	s := &source{containers: []groups.Container{{Name: "web", State: "running"}}}
	m := New(s, nil)
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.err = errors.New("http://user:password@engine?token=SECRET")
	if err := m.Poll(context.Background()); err == nil {
		t.Fatal("expected poll error")
	}
	snap := m.Snapshot()
	if snap.Connected || snap.Error == "" || snap.Error == s.err.Error() || len(snap.Incidents) != 0 {
		t.Fatalf("snapshot %+v", snap)
	}
	got, err := m.Containers()
	if err != nil || len(got) != 1 {
		t.Fatalf("cached data %v %v", got, err)
	}
	s.err = nil
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !m.Snapshot().Connected || m.Snapshot().Error != "" {
		t.Fatal("did not reconnect")
	}
}
