package monitor

import (
	"context"
	"services/internal/groups"
	"testing"
	"time"
)

type deadlineNotifier struct {
	deadlines []time.Time
	monitor   *Monitor
}

func (n *deadlineNotifier) Notify(ctx context.Context, _ Event) error {
	// Reading the snapshot inside a notifier proves no snapshot lock is held.
	_ = n.monitor.Snapshot()
	deadline, _ := ctx.Deadline()
	n.deadlines = append(n.deadlines, deadline)
	time.Sleep(time.Millisecond)
	return nil
}
func TestNotificationBatchSharesOneTimeBudget(t *testing.T) {
	s := &source{containers: []groups.Container{{Name: "a", State: "exited"}, {Name: "b", State: "exited"}}}
	notifier := &deadlineNotifier{}
	m := New(s, notifier)
	notifier.monitor = m
	m.Poll(context.Background())
	if len(notifier.deadlines) != 2 || !notifier.deadlines[0].Equal(notifier.deadlines[1]) {
		t.Fatalf("batch has unbounded per-event budgets: %v", notifier.deadlines)
	}
}
