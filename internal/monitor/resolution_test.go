package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"services/internal/groups"
	"strings"
	"testing"
	"time"
)

func missingFixture(t *testing.T) (*Monitor, *source, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	s := &source{containers: []groups.Container{{ID: "old", Project: "app", Service: "web", Name: "old-web", State: "running"}, {ID: "healthy", Project: "app", Service: "api", Name: "api", State: "running"}}}
	m := New(s, nil)
	m.now = func() time.Time { return now }
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.containers = s.containers[1:]
	now = now.Add(time.Second)
	if err := m.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m, s, &now
}
func decision(m *Monitor, action string) ResolutionDecision {
	i := m.Snapshot().Incidents[0]
	return ResolutionDecision{IncidentID: i.ID, Since: i.Since, Action: action}
}
func TestResolutionSnoozeExpiresAndNotifiesOnlyMissingService(t *testing.T) {
	m, s, now := missingFixture(t)
	ctx := context.Background()
	n := &resolutionNotifier{}
	m.notifier = n
	d := decision(m, "snooze")
	d.SnoozeSeconds = 900
	token, err := m.Resolve(ctx, []ResolutionDecision{d})
	if err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot()
	body, _ := json.Marshal(snap.Incidents[0])
	if !strings.Contains(string(body), "snoozedUntil") || snap.Incidents[0].Status != "missing" {
		t.Fatalf("snooze: %s", body)
	}
	rows, _ := m.Containers()
	body, _ = json.Marshal(rows)
	if !strings.Contains(string(body), "monitorSnoozedUntil") {
		t.Fatal(string(body))
	}
	// Another service must still produce a problem while the missing one is snoozed.
	s.containers[0].Health = "unhealthy"
	*now = now.Add(899 * time.Second)
	m.Poll(ctx)
	if n.problems != 1 {
		t.Fatalf("other service notification %d", n.problems)
	}
	*now = now.Add(time.Second)
	m.Poll(ctx)
	m.Poll(ctx)
	if n.problems != 2 {
		t.Fatalf("expiry must alert exactly once: %d", n.problems)
	}
	if err := m.UndoResolution(ctx, token); !errors.Is(err, ErrResolutionConflict) {
		t.Fatalf("expired undo: %v", err)
	}
}

type resolutionNotifier struct{ problems int }

func (n *resolutionNotifier) Notify(_ context.Context, e Event) error {
	if e.Kind == "problem" {
		n.problems++
	}
	return nil
}
func TestResolutionReplaceHistoryAndRediscovery(t *testing.T) {
	m, s, _ := missingFixture(t)
	ctx := context.Background()
	d := decision(m, "replace")
	d.ReplacementID = "healthy"
	token, err := m.Resolve(ctx, []ResolutionDecision{d})
	if err != nil {
		t.Fatal(err)
	}
	events := m.Snapshot().Events
	if !strings.Contains(events[len(events)-1].Message, "api") {
		t.Fatalf("replacement relationship missing: %+v", events)
	}
	if err := m.UndoResolution(ctx, token); err != nil {
		t.Fatal(err)
	}
	d = decision(m, "replace")
	d.ReplacementID = "healthy"
	_, err = m.Resolve(ctx, []ResolutionDecision{d})
	if err != nil {
		t.Fatal(err)
	}
	s.containers = append(s.containers, groups.Container{ID: "returned", Project: "app", Service: "web", Name: "old-web", State: "running"})
	m.Poll(ctx)
	s.containers = s.containers[:1]
	m.Poll(ctx)
	if len(m.Snapshot().Incidents) != 1 {
		t.Fatal("returned name not rediscovered")
	}
}

func TestResolutionForgetAndUndo(t *testing.T) {
	m, _, _ := missingFixture(t)
	ctx := context.Background()
	token, err := m.Resolve(ctx, []ResolutionDecision{decision(m, "forget")})
	if err != nil || token == "" {
		t.Fatalf("resolve: %q %v", token, err)
	}
	if len(m.Snapshot().Incidents) != 0 {
		t.Fatal("forgotten incident retained")
	}
	rows, _ := m.Containers()
	if len(rows) != 1 || rows[0].ID != "healthy" {
		t.Fatalf("rows: %+v", rows)
	}
	events := m.Snapshot().Events
	if events[len(events)-1].Kind != "resolution" {
		t.Fatalf("not explicit resolution: %+v", events)
	}
	if err := m.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(m.Snapshot().Incidents) != 0 {
		t.Fatal("forgotten expectation returned")
	}
	if err := m.UndoResolution(ctx, token); err != nil {
		t.Fatal(err)
	}
	if got := m.Snapshot().Incidents; len(got) != 1 || got[0].Status != "missing" {
		t.Fatalf("undo: %+v", got)
	}
	if err := m.UndoResolution(ctx, token); err == nil {
		t.Fatal("token reused")
	}
}
