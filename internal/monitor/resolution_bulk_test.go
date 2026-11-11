package monitor

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"services/internal/groups"
)

func TestResolutionMixedBatchAtomicityAndUndo(t *testing.T) {
	m, s, _ := missingFixture(t)
	ctx := context.Background()
	// Establish three independent expectations, all of which disappear together.
	s.containers = append(s.containers, groups.Container{ID: "b", Project: "app", Name: "b", Service: "b", State: "running"}, groups.Container{ID: "c", Project: "app", Name: "c", Service: "c", State: "running"})
	m.Poll(ctx)
	s.containers = s.containers[:1]
	m.Poll(ctx)
	original := m.Snapshot().Incidents
	decisions := []ResolutionDecision{}
	for j, i := range original {
		d := ResolutionDecision{IncidentID: i.ID, Since: i.Since, Action: []string{"forget", "replace", "snooze"}[j]}
		if d.Action == "replace" {
			d.ReplacementID = "healthy"
		}
		if d.Action == "snooze" {
			d.SnoozeSeconds = 900
		}
		decisions = append(decisions, d)
	}
	bad := append([]ResolutionDecision{}, decisions...)
	bad[2].Since = "2020-01-01T00:00:00Z"
	before := m.Snapshot()
	if _, err := m.Resolve(ctx, bad); !errors.Is(err, ErrResolutionConflict) {
		t.Fatalf("bad batch %v", err)
	}
	if !reflect.DeepEqual(before, m.Snapshot()) {
		t.Fatal("partial batch applied")
	}
	duplicate := append([]ResolutionDecision{}, decisions...)
	duplicate[0].Action = "replace"
	duplicate[0].ReplacementID = "healthy"
	if _, err := m.Resolve(ctx, duplicate); !errors.Is(err, ErrInvalidResolution) {
		t.Fatalf("duplicate replacement %v", err)
	}
	token, err := m.Resolve(ctx, decisions)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot().Incidents; len(got) != 1 || got[0].Status != "missing" || got[0].SnoozedUntil == "" {
		t.Fatalf("mixed batch %+v", got)
	}
	if err = m.UndoResolution(ctx, token); err != nil {
		t.Fatal(err)
	}
	got := m.Snapshot().Incidents
	if !reflect.DeepEqual(original, got) {
		t.Fatalf("undo: got %+v want %+v", got, original)
	}
}

type blockedResolutionNotifier struct {
	blocked  bool
	received []Event
}

func (n *blockedResolutionNotifier) Notify(_ context.Context, e Event) error {
	if n.blocked {
		return errors.New("retry")
	}
	n.received = append(n.received, e)
	return nil
}
func TestResolutionSnoozeDropsOnlyMatchingQueuedProblem(t *testing.T) {
	// Same project/name but distinct services must not be snoozed together.
	s := &source{containers: []groups.Container{{ID: "one", Project: "app", Service: "one", Name: "shared", State: "running"}, {ID: "two", Project: "app", Service: "two", Name: "shared", State: "running"}}}
	n := &blockedResolutionNotifier{blocked: true}
	m := New(s, n)
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	ctx := context.Background()
	m.Poll(ctx)
	s.containers = nil
	now = now.Add(time.Second)
	m.Poll(ctx)
	if len(m.pending) != 2 {
		t.Fatalf("pending %v", m.pending)
	}
	d := decision(m, "snooze")
	d.SnoozeSeconds = 900
	if _, err := m.Resolve(ctx, []ResolutionDecision{d}); err != nil {
		t.Fatal(err)
	}
	n.blocked = false
	m.Poll(ctx)
	problems := 0
	for _, e := range n.received {
		if e.Kind == "problem" {
			problems++
		}
	}
	if problems != 1 {
		t.Fatalf("unrelated pending notification lost: %+v", n.received)
	}
	now = now.Add(900 * time.Second)
	m.Poll(ctx)
	m.Poll(ctx)
	problems = 0
	for _, e := range n.received {
		if e.Kind == "problem" {
			problems++
		}
	}
	if problems != 2 {
		t.Fatalf("expiry notification count %d: %+v", problems, n.received)
	}
}
func TestResolutionContainerCopiesDoNotAliasPublication(t *testing.T) {
	m, s, _ := missingFixture(t)
	ctx := context.Background()
	s.containers[0].UpdatePublication = &groups.ImagePublication{Tags: []string{"safe"}}
	m.Poll(ctx)
	rows, _ := m.Containers()
	rows[0].UpdatePublication.Tags[0] = "mutated"
	rows, _ = m.Containers()
	if rows[0].UpdatePublication.Tags[0] != "safe" {
		t.Fatal("snapshot publication aliases caller")
	}
}
func TestResolutionSnoozeRecoveryDoesNotRealertAndUndoRejectsReturned(t *testing.T) {
	m, s, now := missingFixture(t)
	ctx := context.Background()
	d := decision(m, "snooze")
	d.SnoozeSeconds = 900
	old := m.known[d.IncidentID]
	n := &resolutionNotifier{}
	m.notifier = n
	token, err := m.Resolve(ctx, []ResolutionDecision{d})
	if err != nil {
		t.Fatal(err)
	}
	// Fresh validation notices the returned identity before a poll.
	s.containers = append(s.containers, old)
	if err = m.UndoResolution(ctx, token); !errors.Is(err, ErrResolutionConflict) {
		t.Fatalf("returned undo %v", err)
	}
	m.Poll(ctx)
	*now = now.Add(901 * time.Second)
	m.Poll(ctx)
	if n.problems != 0 || len(m.Snapshot().Incidents) != 0 {
		t.Fatal("recovered snoozed service alerted")
	}
	for _, c := range mustContainers(t, m) {
		if c.MonitorSnoozedUntil != "" {
			t.Fatal("snooze leaked to healthy row")
		}
	}
}
func mustContainers(t *testing.T, m *Monitor) []groups.Container {
	t.Helper()
	rows, err := m.Containers()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
