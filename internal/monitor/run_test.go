package monitor

import (
	"context"
	"services/internal/groups"
	"sync/atomic"
	"testing"
	"time"
)

type countingSource struct{ calls atomic.Int32 }

func (s *countingSource) Containers() ([]groups.Container, error) {
	s.calls.Add(1)
	return []groups.Container{{Name: "web", State: "running", RepoDigests: []string{"original"}}}, nil
}
func TestRunPollsWithoutReadersAndStopsOnCancellation(t *testing.T) {
	s := &countingSource{}
	m := New(s, nil)
	m.interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	deadline := time.After(time.Second)
	for s.calls.Load() < 3 {
		select {
		case <-deadline:
			t.Fatal("background did not poll")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run did not stop")
	}
	got, _ := m.Containers()
	got[0].RepoDigests[0] = "mutated"
	got2, _ := m.Containers()
	if got2[0].RepoDigests[0] != "original" {
		t.Fatal("cache aliases caller")
	}
}

type cancelSource struct{ called chan struct{} }

func (s *cancelSource) Containers() ([]groups.Container, error) {
	panic("must prefer context-aware source")
}
func (s *cancelSource) ContainersContext(ctx context.Context) ([]groups.Container, error) {
	close(s.called)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestRunCancelsContextAwareSource(t *testing.T) {
	s := &cancelSource{called: make(chan struct{})}
	m := New(s, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	select {
	case <-s.called:
	case <-time.After(time.Second):
		t.Fatal("context-aware source not called")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("source not canceled")
	}
}
func TestHistoryBounded(t *testing.T) {
	s := &source{containers: []groups.Container{{Name: "web", State: "running"}}}
	m := New(s, nil)
	for i := 0; i < 150; i++ {
		if i%2 == 0 {
			s.containers[0].State = "exited"
		} else {
			s.containers[0].State = "running"
		}
		m.Poll(context.Background())
	}
	if len(m.Snapshot().Events) != 100 {
		t.Fatalf("history length %d", len(m.Snapshot().Events))
	}
	snap := m.Snapshot()
	snap.Events[0].Message = "mutated"
	if m.Snapshot().Events[0].Message == "mutated" {
		t.Fatal("snapshot aliases caller")
	}
}
