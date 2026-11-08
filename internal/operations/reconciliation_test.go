package operations

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

type trackingWorkers struct {
	containers map[string]Inspect
	launches   []Operation
	starts     int
}

func (f *trackingWorkers) Inspect(_ context.Context, name string) (Inspect, error) {
	v, ok := f.containers[name]
	if !ok {
		return Inspect{}, ErrNotFound
	}
	return v, nil
}
func (f *trackingWorkers) Launch(_ context.Context, _ WorkerConfig, o Operation) error {
	f.launches = append(f.launches, o)
	var v Inspect
	v.State.Running = true
	v.State.Status = "running"
	v.Config.Labels = map[string]string{"io.services.worker": "true", "io.services.operation": o.ID}
	f.containers[o.Worker] = v
	return nil
}
func (f *trackingWorkers) Start(_ context.Context, name string) error {
	f.starts++
	v := f.containers[name]
	v.State.Running = true
	v.State.Status = "running"
	f.containers[name] = v
	return nil
}
func (f *trackingWorkers) Remove(_ context.Context, name string) error {
	delete(f.containers, name)
	return nil
}
func TestReopenedCoordinatorReattachesAndWaitsForHelperExit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ops.db")
	s, _ := Open(p)
	first, _ := s.Submit("a", "update project", "", "")
	second, _ := s.Submit("b", "update project", "", "")
	f := &trackingWorkers{containers: map[string]Inspect{}}
	c := &Coordinator{Store: s, Workers: f}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, _ = Open(p)
	defer s.Close()
	c = &Coordinator{Store: s, Workers: f}
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 1 {
		t.Fatal("restart launched overlapping helper")
	}
	s.Finish(first.ID, nil, false)
	c.Tick(context.Background())
	if len(f.launches) != 1 {
		t.Fatal("advanced before helper exited")
	}
	v := f.containers[first.Worker]
	v.State.Running = false
	v.State.Status = "exited"
	f.containers[first.Worker] = v
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.launches) != 2 || f.launches[1].ID != second.ID {
		t.Fatal("durable FIFO did not advance")
	}
}
func TestExitedWorkerWithoutTerminalResultRequiresAttention(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "ops.db"))
	defer s.Close()
	o, _ := s.Submit("a", "update project", "", "")
	f := &trackingWorkers{containers: map[string]Inspect{}}
	c := Coordinator{Store: s, Workers: f}
	c.Tick(context.Background())
	v := f.containers[o.Worker]
	v.State.Status = "exited"
	v.State.Running = false
	f.containers[o.Worker] = v
	if err := c.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(o.ID)
	if got.State != "error" || !got.NeedsAttention {
		t.Fatalf("uncertain exit marked successful: %+v", got)
	}
}
func TestOwnedCreatedWorkerStartsButForeignWorkerDoesNot(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "ops.db"))
	defer s.Close()
	o, _ := s.Submit("a", "update project", "", "")
	s.Claim()
	var v Inspect
	v.State.Status = "created"
	v.Config.Labels = map[string]string{"io.services.worker": "true", "io.services.operation": o.ID}
	f := &trackingWorkers{containers: map[string]Inspect{o.Worker: v}}
	c := Coordinator{Store: s, Workers: f}
	if err := c.Tick(context.Background()); err != nil || f.starts != 1 || len(f.launches) != 0 {
		t.Fatal(err, f.starts)
	}
	v.Config.Labels["io.services.operation"] = "foreign"
	f.containers[o.Worker] = v
	if err := c.Tick(context.Background()); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatal("foreign helper accepted", err)
	}
	if f.starts != 1 {
		t.Fatal("foreign helper started")
	}
}
