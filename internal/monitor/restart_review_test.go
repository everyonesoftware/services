package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"services/internal/groups"
)

func TestRestartWindowBaselinesAfterAmbiguousObservationGap(t *testing.T) {
	for _, gap := range []string{"outage", "missing", "no-poll"} {
		t.Run(gap, func(t *testing.T) {
			original := groups.Container{ID: "a", Name: "web", State: "running", RestartCount: 40}
			s := &source{containers: []groups.Container{original}}
			m := New(s, nil)
			now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
			m.now = func() time.Time { return now }
			poll := func() {
				t.Helper()
				if err := m.Poll(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			poll()
			now = now.Add(time.Minute)
			s.containers[0].RestartCount = 42
			poll()
			now = now.Add(2 * time.Hour)
			switch gap {
			case "outage":
				s.err = errors.New("offline")
				if err := m.Poll(context.Background()); err == nil {
					t.Fatal("expected outage")
				}
				s.err = nil
			case "missing":
				s.containers = nil
				poll()
			}
			now = now.Add(time.Minute)
			original.RestartCount = 100
			s.containers = []groups.Container{original}
			poll()
			got, _ := m.Containers()
			if got[0].MonitorStatus != "running" {
				t.Fatalf("ambiguous changes attributed to five minutes: %+v", got[0])
			}
			for i := 1; i <= 3; i++ {
				now = now.Add(time.Minute)
				s.containers[0].RestartCount++
				poll()
				got, _ = m.Containers()
				want := "running"
				if i == 3 {
					want = "restart-loop"
				}
				if got[0].MonitorStatus != want {
					t.Fatalf("post-gap sample %d: %+v", i, got[0])
				}
			}
		})
	}
}

func TestRestartWindowExpiresFromObservationIntervalStart(t *testing.T) {
	s := &source{containers: []groups.Container{{ID: "a", Name: "web", State: "running"}}}
	m := New(s, nil)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Minute)
	s.containers[0].RestartCount = 3
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Containers()
	if got[0].MonitorStatus != "restart-loop" {
		t.Fatalf("bounded interval not counted: %+v", got)
	}
	now = now.Add(time.Minute)
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Containers()
	if got[0].MonitorStatus != "running" {
		t.Fatalf("cannot assert changes occurred in current window: %+v", got)
	}
}

func TestRestartWindowCountsObservedLifecycleChurn(t *testing.T) {
	for _, kind := range []string{"replacement", "started-at", "counter-reset", "combined"} {
		t.Run(kind, func(t *testing.T) {
			s := &source{containers: []groups.Container{{ID: "a", Name: "web", State: "running", RestartCount: 9, StartedAt: "initial"}}}
			m := New(s, nil)
			now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
			m.now = func() time.Time { return now }
			poll := func() {
				t.Helper()
				if err := m.Poll(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			poll()
			for i, id := range []string{"b", "c", "d"} {
				now = now.Add(time.Minute)
				switch kind {
				case "replacement":
					s.containers[0].ID = id
				case "started-at":
					s.containers[0].StartedAt = id
				case "counter-reset":
					s.containers[0].RestartCount--
				case "combined":
					s.containers[0].ID = id
					s.containers[0].StartedAt = id
					s.containers[0].RestartCount--
				}
				poll()
				got, _ := m.Containers()
				want := "running"
				if i == 2 {
					want = "restart-loop"
				}
				if got[0].MonitorStatus != want {
					t.Fatalf("transition %d: want %s, got %+v", i+1, want, got[0])
				}
			}
			now = now.Add(6 * time.Minute)
			poll()
			got, _ := m.Containers()
			if got[0].MonitorStatus != "running" {
				t.Fatalf("churn did not expire: %+v", got)
			}
		})
	}
}

func TestRestartWindowDoesNotDoubleCountLifecycleAndPositiveDelta(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-container", true: "replacement-counter-is-not-a-delta"}[replacement], func(t *testing.T) {
			s := &source{containers: []groups.Container{{ID: "a", Name: "web", State: "running", StartedAt: "initial"}}}
			m := New(s, nil)
			now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
			m.now = func() time.Time { return now }
			if err := m.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			for i, id := range []string{"b", "c", "d"} {
				now = now.Add(time.Minute)
				s.containers[0].StartedAt = id
				s.containers[0].RestartCount++
				if replacement {
					s.containers[0].ID = id
					s.containers[0].RestartCount += 40
				}
				if err := m.Poll(context.Background()); err != nil {
					t.Fatal(err)
				}
				got, _ := m.Containers()
				want := "running"
				if i == 2 {
					want = "restart-loop"
				}
				if got[0].MonitorStatus != want {
					t.Fatalf("sample %d: want %s, got %+v", i+1, want, got[0])
				}
			}
		})
	}
}

func TestRestartWindowSingleReplacementRemainsRunning(t *testing.T) {
	s := &source{containers: []groups.Container{{ID: "old", Name: "web", State: "running", RestartCount: 5, StartedAt: "old-start"}}}
	m := New(s, nil)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.containers[0].ID = "deployment"
	s.containers[0].RestartCount = 0
	s.containers[0].StartedAt = "deployed"
	for i := 0; i < 8; i++ {
		now = now.Add(time.Minute)
		if err := m.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		got, _ := m.Containers()
		if got[0].MonitorStatus != "running" || len(m.Snapshot().Incidents) != 0 {
			t.Fatalf("one deployment caused alert on poll %d: %+v", i, got)
		}
	}
}

func TestRestartWindowPreservesCounterSamplesAcrossReplacement(t *testing.T) {
	s := &source{containers: []groups.Container{{ID: "old", Name: "web", State: "running"}}}
	m := New(s, nil)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	s.containers[0].RestartCount = 2
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	s.containers[0].ID = "new"
	s.containers[0].RestartCount = 0
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Containers()
	if got[0].MonitorStatus != "restart-loop" {
		t.Fatalf("lost pre-replacement evidence: %+v", got)
	}
	now = now.Add(3 * time.Minute)
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ = m.Containers()
	if got[0].MonitorStatus != "running" {
		t.Fatalf("expired counter evidence retained: %+v", got)
	}
}

func TestRestartWindowBaselinesLifecycleAfterLongGap(t *testing.T) {
	s := &source{containers: []groups.Container{{ID: "a", Name: "web", State: "running", RestartCount: 10, StartedAt: "initial"}}}
	m := New(s, nil)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	for i, id := range []string{"b", "c", "d", "e"} {
		s.containers[0].ID = id
		s.containers[0].StartedAt = id
		s.containers[0].RestartCount--
		if err := m.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
		got, _ := m.Containers()
		want := "running"
		if i == 3 {
			want = "restart-loop"
		}
		if got[0].MonitorStatus != want {
			t.Fatalf("long-gap observation %d: want %s, got %+v", i, want, got)
		}
		now = now.Add(time.Minute)
	}
}

func TestRestartFallbackRespectsConfiguredHealth(t *testing.T) {
	for _, health := range []string{"healthy", "starting", "unhealthy", "unknown"} {
		t.Run(health, func(t *testing.T) {
			s := &source{containers: []groups.Container{{ID: "a", Name: "web", State: "running", Health: health, StartedAt: "original", RestartCount: 40}}}
			m := New(s, nil)
			now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
			m.now = func() time.Time { return now }
			if err := m.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
			s.containers[0].RestartCount = 43
			s.containers[0].StartedAt = "restarted"
			if err := m.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, _ := m.Containers()
			want, reason := classify(s.containers[0])
			if got[0].MonitorStatus != want || got[0].MonitorReason != reason {
				t.Fatalf("health authority lost: %+v", got[0])
			}
			if got[0].RestartCount != 43 || got[0].StartedAt != "restarted" {
				t.Fatalf("lost restart diagnostics: %+v", got[0])
			}
		})
	}
}
