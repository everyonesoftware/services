package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"services/internal/groups"
)

func TestResolutionRejectsMalformedExpectedSnooze(t *testing.T) {
	m, _, _ := missingFixture(t)
	d := decision(m, "forget")
	b, _ := json.Marshal(d)
	var fields map[string]any
	json.Unmarshal(b, &fields)
	fields["snoozedUntil"] = "not-a-date"
	b, _ = json.Marshal(fields)
	json.Unmarshal(b, &d)
	if _, err := m.Resolve(context.Background(), []ResolutionDecision{d}); !errors.Is(err, ErrInvalidResolution) {
		t.Fatalf("malformed snooze: %v", err)
	}
}
func TestResolutionAllSnoozeDurationsExpireExactlyOnce(t *testing.T) {
	for _, seconds := range []int{900, 3600, 86400} {
		t.Run((time.Duration(seconds) * time.Second).String(), func(t *testing.T) {
			m, _, now := missingFixture(t)
			d := decision(m, "snooze")
			d.SnoozeSeconds = seconds
			n := &resolutionNotifier{}
			m.notifier = n
			_, err := m.Resolve(context.Background(), []ResolutionDecision{d})
			if err != nil {
				t.Fatal(err)
			}
			deadline := now.Add(time.Duration(seconds) * time.Second)
			*now = deadline.Add(-time.Nanosecond)
			m.Poll(context.Background())
			if n.problems != 0 {
				t.Fatal("alerted before expiry")
			}
			*now = deadline
			m.Poll(context.Background())
			m.Poll(context.Background())
			if n.problems != 1 || m.Snapshot().Incidents[0].SnoozedUntil != "" {
				t.Fatalf("expiry: %d %+v", n.problems, m.Snapshot())
			}
		})
	}
}
func TestResolutionUndoStoreIsBounded(t *testing.T) {
	m, s, _ := missingFixture(t)
	ctx := context.Background()
	// Repeatedly rediscover new names, forget them, and retain unused receipts.
	for j := 0; j < 105; j++ {
		if j > 0 {
			c := groups.Container{ID: time.Duration(j).String(), Project: "app", Service: "web", Name: time.Duration(j).String(), State: "running"}
			s.containers = append(s.containers, c)
			m.Poll(ctx)
			s.containers = s.containers[:1]
			m.Poll(ctx)
		}
		if _, err := m.Resolve(ctx, []ResolutionDecision{decision(m, "forget")}); err != nil {
			t.Fatal(err)
		}
	}
	if len(m.resolutions) != 100 {
		t.Fatalf("undo receipts: %d", len(m.resolutions))
	}
}

// The contextual source is immutable while concurrent operations exercise the
// actual poll serialization. Snapshot readers must remain usable meanwhile.
type serializedResolutionSource struct {
	rows    []groups.Container
	active  atomic.Int32
	overlap atomic.Bool
	calls   atomic.Int32
}

func (s *serializedResolutionSource) Containers() ([]groups.Container, error) {
	panic("must use contextual source")
}
func (s *serializedResolutionSource) ContainersContext(ctx context.Context) ([]groups.Container, error) {
	if s.active.Add(1) > 1 {
		s.overlap.Store(true)
	}
	defer s.active.Add(-1)
	s.calls.Add(1)
	if _, ok := ctx.Deadline(); !ok {
		panic("missing timeout")
	}
	return s.rows, nil
}
func TestResolutionFreshReadSharesPollSerialization(t *testing.T) {
	m, raw, _ := missingFixture(t)
	s := &serializedResolutionSource{rows: cloneContainers(raw.containers)}
	m.source = s
	d := decision(m, "forget")
	var wg sync.WaitGroup
	for j := 0; j < 20; j++ {
		wg.Add(1)
		go func() { defer wg.Done(); m.Snapshot(); m.Containers(); m.Poll(context.Background()) }()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		token, err := m.Resolve(context.Background(), []ResolutionDecision{d})
		if err != nil {
			t.Error(err)
			return
		}
		if err = m.UndoResolution(context.Background(), token); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	if s.overlap.Load() {
		t.Fatal("engine reads overlapped")
	}
	if s.calls.Load() != 22 {
		t.Fatalf("fresh source calls %d", s.calls.Load())
	}
}
func TestResolutionUndoRejectsReplacementThatChanged(t *testing.T) {
	m, s, _ := missingFixture(t)
	d := decision(m, "replace")
	d.ReplacementID = "healthy"
	ctx := context.Background()
	token, err := m.Resolve(ctx, []ResolutionDecision{d})
	if err != nil {
		t.Fatal(err)
	}
	s.containers[0].ID = "new-api" // same identity, recreated after review
	if err = m.UndoResolution(ctx, token); !errors.Is(err, ErrResolutionConflict) {
		t.Fatalf("replacement changed: %v", err)
	}
}
