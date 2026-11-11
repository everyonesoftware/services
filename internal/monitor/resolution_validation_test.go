package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// Populate the optimistic snooze version through JSON so this starts RED even
// before the optional contract field exists.
func reviewedDecision(m *Monitor, action string) ResolutionDecision {
	i := m.Snapshot().Incidents[0]
	b, _ := json.Marshal(map[string]any{"incidentId": i.ID, "since": i.Since, "action": action, "snoozedUntil": i.SnoozedUntil})
	var d ResolutionDecision
	_ = json.Unmarshal(b, &d)
	return d
}
func TestResolutionFreshSnoozedDecisionSupersedesOldUndo(t *testing.T) {
	for _, action := range []string{"forget", "replace", "snooze"} {
		t.Run(action, func(t *testing.T) {
			m, _, _ := missingFixture(t)
			ctx := context.Background()
			stale := decision(m, "forget")
			d := decision(m, "snooze")
			d.SnoozeSeconds = 900
			old, err := m.Resolve(ctx, []ResolutionDecision{d})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Resolve(ctx, []ResolutionDecision{stale}); !errors.Is(err, ErrResolutionConflict) {
				t.Fatalf("cross-tab stale %v", err)
			}
			fresh := reviewedDecision(m, action)
			if action == "replace" {
				fresh.ReplacementID = "healthy"
			}
			if action == "snooze" {
				fresh.SnoozeSeconds = 3600
			}
			token, err := m.Resolve(ctx, []ResolutionDecision{fresh})
			if err != nil {
				t.Fatalf("fresh decision: %v", err)
			}
			if err = m.UndoResolution(ctx, old); !errors.Is(err, ErrResolutionConflict) {
				t.Fatalf("old undo %v", err)
			}
			if err = m.UndoResolution(ctx, token); err != nil {
				t.Fatal(err)
			}
			if got := m.Snapshot().Incidents[0]; got.SnoozedUntil == "" {
				t.Fatal("undo lost original snooze")
			}
		})
	}
}
func TestResolutionRejectsInvalidAndStaleBatchesAtomically(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Monitor, *source, ResolutionDecision) []ResolutionDecision
		want  error
	}{
		{"empty", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision { return nil }, ErrInvalidResolution},
		{"unknown action", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			d.Action = "delete"
			return []ResolutionDecision{d}
		}, ErrInvalidResolution},
		{"duration", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			d.Action = "snooze"
			d.SnoozeSeconds = 60
			return []ResolutionDecision{d}
		}, ErrInvalidResolution},
		{"extra field", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			d.ReplacementID = "healthy"
			return []ResolutionDecision{d}
		}, ErrInvalidResolution},
		{"duplicate", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			return []ResolutionDecision{d, d}
		}, ErrInvalidResolution},
		{"stale since", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			d.Since = "2026-01-01T00:00:00Z"
			return []ResolutionDecision{d}
		}, ErrResolutionConflict},
		{"missing incident", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			d.IncidentID = "unknown"
			return []ResolutionDecision{decision(m, "forget"), d}
		}, ErrResolutionConflict},
		{"replacement missing", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			d.Action = "replace"
			d.ReplacementID = "gone"
			return []ResolutionDecision{d}
		}, ErrResolutionConflict},
		{"other project", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			s.containers[0].Project = "other"
			d.Action = "replace"
			d.ReplacementID = "healthy"
			return []ResolutionDecision{d}
		}, ErrInvalidResolution},
		{"self replacement", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			s.containers[0].ID = "old"
			d.Action = "replace"
			d.ReplacementID = "old"
			return []ResolutionDecision{d}
		}, ErrInvalidResolution},
		{"recovered before poll", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			s.containers = append(s.containers, m.known[d.IncidentID])
			return []ResolutionDecision{d}
		}, ErrResolutionConflict},
		{"engine outage", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			s.err = errors.New("SECRET engine unavailable")
			return []ResolutionDecision{d}
		}, ErrResolutionUnavailable},
		{"healthy current", func(m *Monitor, s *source, d ResolutionDecision) []ResolutionDecision {
			d.IncidentID = identity(s.containers[0])
			return []ResolutionDecision{d}
		}, ErrResolutionConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, s, _ := missingFixture(t)
			batch := tc.setup(m, s, decision(m, "forget"))
			before := m.Snapshot()
			rows, _ := m.Containers()
			token, err := m.Resolve(context.Background(), batch)
			if !errors.Is(err, tc.want) || token != "" {
				t.Fatalf("token %q err %v want %v", token, err, tc.want)
			}
			afterRows, _ := m.Containers()
			if !reflect.DeepEqual(before, m.Snapshot()) || !reflect.DeepEqual(rows, afterRows) {
				t.Fatal("failed batch changed monitor")
			}
		})
	}
}
func TestResolutionUndoFreshnessAndUnrelatedChanges(t *testing.T) {
	for _, returned := range []bool{false, true} {
		t.Run(map[bool]string{false: "unrelated", true: "returned"}[returned], func(t *testing.T) {
			m, s, _ := missingFixture(t)
			ctx := context.Background()
			d := decision(m, "forget")
			old := m.known[d.IncidentID]
			token, err := m.Resolve(ctx, []ResolutionDecision{d})
			if err != nil {
				t.Fatal(err)
			}
			s.containers[0].Health = "unhealthy"
			if returned {
				s.containers = append(s.containers, old)
			}
			m.Poll(ctx)
			if returned {
				s.containers = s.containers[:1]
				m.Poll(ctx)
			}
			err = m.UndoResolution(ctx, token)
			if returned {
				if !errors.Is(err, ErrResolutionConflict) {
					t.Fatalf("returned undo: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(m.Snapshot().Incidents) != 2 {
				t.Fatal("unrelated incident clobbered")
			}
		})
	}
}
func TestResolutionConcurrentTabsOnlyOneSucceeds(t *testing.T) {
	m, _, _ := missingFixture(t)
	d := decision(m, "forget")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for j := 0; j < 2; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Resolve(context.Background(), []ResolutionDecision{d})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrResolutionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success %d conflict %d", success, conflicts)
	}
}
func TestResolutionUnknownUndoTokenConflictsEvenDuringOutage(t *testing.T) {
	m, s, _ := missingFixture(t)
	s.err = errors.New("outage")
	if err := m.UndoResolution(context.Background(), "unknown"); !errors.Is(err, ErrResolutionConflict) {
		t.Fatalf("unknown token: %v", err)
	}
}

func TestResolutionUndoExpiredUnknownAndOutage(t *testing.T) {
	m, s, now := missingFixture(t)
	ctx := context.Background()
	token, err := m.Resolve(ctx, []ResolutionDecision{decision(m, "forget")})
	if err != nil {
		t.Fatal(err)
	}
	s.err = errors.New("outage")
	if err = m.UndoResolution(ctx, token); !errors.Is(err, ErrResolutionUnavailable) {
		t.Fatal(err)
	}
	s.err = nil
	if err = m.UndoResolution(ctx, "bad-token"); !errors.Is(err, ErrResolutionConflict) {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Minute)
	if err = m.UndoResolution(ctx, token); !errors.Is(err, ErrResolutionConflict) {
		t.Fatal(err)
	}
}
