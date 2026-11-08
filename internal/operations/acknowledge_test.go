package operations

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestAcknowledgementOnlyResumesAfterStoppedOwnedWorker(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, _ := s.Submit("alpha", "update project", "", "")
	s.Claim()
	s.Finish(first.ID, errors.New("uncertain outcome"), true)
	second, _ := s.Submit("beta", "update project", "", "")
	var inspected Inspect
	inspected.Config.Labels = map[string]string{"io.services.worker": "true", "io.services.operation": first.ID}
	inspected.State.Running = true
	inspected.State.Status = "running"
	workers := &trackingWorkers{containers: map[string]Inspect{first.Worker: inspected}}
	c := Coordinator{Store: s, Workers: workers}
	if err := c.Acknowledge(context.Background(), first.ID); err == nil {
		t.Fatal("acknowledged active worker")
	}
	if got, _ := s.Claim(); got != nil {
		t.Fatal("advanced despite active helper")
	}
	inspected.State.Running = false
	inspected.State.Status = "exited"
	workers.containers[first.Worker] = inspected
	if err := c.Acknowledge(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(first.ID)
	if got.NeedsAttention || got.State != "error" || got.Error != "uncertain outcome" {
		t.Fatalf("acknowledgement rewrote outcome: %+v", got)
	}
	next, err := s.Claim()
	if err != nil || next == nil || next.ID != second.ID {
		t.Fatalf("queue did not resume: %+v %v", next, err)
	}
	if ok, _ := s.BeginExecution(first.ID); ok {
		t.Fatal("acknowledgement replayed interrupted job")
	}
}

func TestAcknowledgementRejectsForeignWorkerAndDockerFailure(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "operations.db"))
	defer s.Close()
	o, _ := s.Submit("alpha", "update project", "", "")
	s.Claim()
	s.Finish(o.ID, errors.New("uncertain"), true)
	var inspected Inspect
	inspected.State.Status = "exited"
	inspected.Config.Labels = map[string]string{"io.services.worker": "true", "io.services.operation": "foreign"}
	c := Coordinator{Store: s, Workers: &trackingWorkers{containers: map[string]Inspect{o.Worker: inspected}}}
	if err := c.Acknowledge(context.Background(), o.ID); err == nil {
		t.Fatal("acknowledged foreign helper")
	}
	c.Workers = &unavailableWorkers{}
	if err := c.Acknowledge(context.Background(), o.ID); err == nil {
		t.Fatal("acknowledged Docker outage")
	}
	got, _ := s.Get(o.ID)
	if !got.NeedsAttention {
		t.Fatal("lost attention gate")
	}
}

type unavailableWorkers struct{ trackingWorkers }

func (*unavailableWorkers) Inspect(context.Context, string) (Inspect, error) {
	return Inspect{}, errors.New("Docker unavailable")
}

func TestAcknowledgementOfMissingWorkerIsDurableAndIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "operations.db")
	s, _ := Open(p)
	o, _ := s.Submit("alpha", "update project", "", "")
	s.Claim()
	s.Finish(o.ID, errors.New("uncertain"), true)
	c := Coordinator{Store: s, Workers: &trackingWorkers{containers: map[string]Inspect{}}}
	if err := c.Acknowledge(context.Background(), o.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.Acknowledge(context.Background(), o.ID); err != nil {
		t.Fatal("acknowledgement retry must be safe", err)
	}
	s.Close()
	s, _ = Open(p)
	defer s.Close()
	got, _ := s.Get(o.ID)
	if got.NeedsAttention || got.State != "error" {
		t.Fatalf("lost acknowledgement: %+v", got)
	}
	queued, _ := s.Submit("beta", "update project", "", "")
	if err := (&Coordinator{Store: s, Workers: c.Workers}).Acknowledge(context.Background(), queued.ID); err == nil {
		t.Fatal("acknowledged nonterminal operation")
	}
}
