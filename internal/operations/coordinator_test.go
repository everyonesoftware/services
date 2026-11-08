package operations

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type fakeWorkers struct {
	status   string
	launches int
	missing  bool
}

func (f *fakeWorkers) Inspect(context.Context, string) (Inspect, error) {
	if f.missing {
		return Inspect{}, ErrNotFound
	}
	var i Inspect
	i.State.Status = f.status
	i.State.Running = f.status == "running"
	i.Config.Labels = map[string]string{"io.services.worker": "true", "io.services.operation": f.status}
	return i, nil
}
func (f *fakeWorkers) Launch(context.Context, WorkerConfig, Operation) error {
	f.launches++
	return nil
}
func (f *fakeWorkers) Start(context.Context, string) error  { return nil }
func (f *fakeWorkers) Remove(context.Context, string) error { return nil }
func TestReconcileMissingWorkerNeverReplaysUncertainWork(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, _ := s.Submit("a", "update project", "", "")
	s.Claim()
	s.Submit("b", "update project", "", "")
	f := &fakeWorkers{missing: true}
	c := Coordinator{Store: s, Workers: f}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	o, _ := s.Get(first.ID)
	if o.State != "error" || !o.NeedsAttention || o.Phase != "interrupted" {
		t.Fatalf("uncertain outcome: %+v", o)
	}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.launches != 0 {
		t.Fatal("uncertain work or queued follower replayed")
	}
}
func TestExecutionClaimIsAtMostOnceAcrossReopen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ops.db")
	s, _ := Open(p)
	o, _ := s.Submit("a", "update project", "", "")
	s.Claim()
	ok, err := s.BeginExecution(o.ID)
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	s.Close()
	s, _ = Open(p)
	defer s.Close()
	ok, err = s.BeginExecution(o.ID)
	if ok || err != nil {
		t.Fatal("worker replayed", ok, err)
	}
	s.Finish(o.ID, errors.New("interrupted"), true)
	if next, _ := s.Claim(); next != nil {
		t.Fatal("attention must hold queue")
	}
}
