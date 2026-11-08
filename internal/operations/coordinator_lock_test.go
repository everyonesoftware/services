package operations

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The launch barrier is after the durable claim but before helper creation.
// Both coordinators use independent database handles, as separate servers do.
type blockedLaunchWorkers struct {
	mu         sync.Mutex
	entered    chan struct{}
	release    chan struct{}
	containers map[string]Inspect
	launches   int
	journal    *Store
	starts     int
}

func (f *blockedLaunchWorkers) Inspect(_ context.Context, name string) (Inspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.containers[name]
	if !ok {
		return Inspect{}, ErrNotFound
	}
	return v, nil
}
func (f *blockedLaunchWorkers) Launch(ctx context.Context, _ WorkerConfig, o Operation) error {
	f.mu.Lock()
	f.launches++
	f.mu.Unlock()
	return f.create(ctx, o)
}
func (f *blockedLaunchWorkers) create(ctx context.Context, o Operation) error {
	close(f.entered)
	select {
	case <-f.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	// A real helper writes its execution claim while Launch is still in flight.
	ok, err := f.journal.BeginExecution(o.ID)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("healthy helper execution claim rejected")
	}
	var v Inspect
	v.State.Running = true
	v.State.Status = "running"
	v.Config.Labels = map[string]string{"io.services.worker": "true", "io.services.operation": o.ID}
	f.mu.Lock()
	f.containers[o.Worker] = v
	f.mu.Unlock()
	return nil
}
func (f *blockedLaunchWorkers) Start(ctx context.Context, name string) error {
	f.mu.Lock()
	f.starts++
	id := f.containers[name].Config.Labels["io.services.operation"]
	f.mu.Unlock()
	o, err := f.journal.Get(id)
	if err != nil {
		return err
	}
	return f.create(ctx, o)
}
func (f *blockedLaunchWorkers) Remove(context.Context, string) error { return nil }

func TestCoordinatorsSerializeClaimThroughLaunchAcrossStores(t *testing.T) {
	for _, stage := range []string{"launch", "start"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "operations.db")
			a, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			first, err := a.Submit("first", "update project", "", "")
			if err != nil {
				t.Fatal(err)
			}
			follower, err := a.Submit("second", "update project", "", "")
			if err != nil {
				t.Fatal(err)
			}
			f := &blockedLaunchWorkers{entered: make(chan struct{}), release: make(chan struct{}), containers: map[string]Inspect{}, journal: b}
			if stage == "start" {
				claimed, err := a.Claim()
				if err != nil || claimed == nil || claimed.ID != first.ID {
					t.Fatalf("claim: %+v, %v", claimed, err)
				}
				var created Inspect
				created.State.Status = "created"
				created.Config.Labels = map[string]string{"io.services.worker": "true", "io.services.operation": first.ID}
				f.containers[first.Worker] = created
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- (&Coordinator{Store: a, Workers: f}).Tick(ctx) }()
			var release sync.Once
			joined := false
			defer func() {
				release.Do(func() { close(f.release) })
				if !joined {
					if err := <-done; err != nil {
						t.Error(err)
					}
				}
			}()
			select {
			case <-f.entered:
			case <-ctx.Done():
				t.Fatal("first coordinator never reached Launch")
			}

			waiting, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			defer stop()
			err = (&Coordinator{Store: b, Workers: f}).Tick(waiting)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("competing Tick must wait for claim/create serialization until its context expires; got %v", err)
			}
			got, err := b.Get(first.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != "running" || got.NeedsAttention {
				t.Fatalf("competing coordinator interrupted healthy in-flight launch: %+v", got)
			}
			release.Do(func() { close(f.release) })
			err = <-done
			joined = true
			if err != nil {
				t.Fatal(err)
			}
			if err := (&Coordinator{Store: b, Workers: f}).Tick(ctx); err != nil {
				t.Fatal(err)
			}
			got, err = b.Get(first.ID)
			if err != nil || got.State != "running" || got.NeedsAttention {
				t.Fatalf("reattach: %+v, %v", got, err)
			}
			next, err := b.Get(follower.ID)
			if err != nil || next.State != "queued" {
				t.Fatalf("follower advanced: %+v, %v", next, err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			wantLaunches, wantStarts := 1, 0
			if stage == "start" {
				wantLaunches, wantStarts = 0, 1
			}
			if f.launches != wantLaunches || f.starts != wantStarts {
				t.Fatalf("launches=%d starts=%d, want launches=%d starts=%d", f.launches, f.starts, wantLaunches, wantStarts)
			}
		})
	}
}
