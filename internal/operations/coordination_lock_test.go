package operations

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCoordinatorLockAcquisitionCancellationReleaseCloseAndReopen(t *testing.T) {
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
	owner, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	unlock, err := a.lockCoordinator(owner)
	if err != nil {
		t.Fatal(err)
	}
	// Cancellation must not release ownership while a helper API call could
	// still be returning. Only the enclosing Tick's deferred unlock may do so.
	cancelOwner()
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	// Both SQLite contention and waiting for this handle's sole connection must
	// observe the context, without waiting for the journal's 10s busy timeout.
	for _, contender := range []*Store{a, b} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		_, err := contender.lockCoordinator(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiting acquisition: %v", err)
		}
		if time.Since(start) > time.Second {
			t.Fatal("lock acquisition ignored deadline")
		}
	}
	unlock()
	unlock = nil
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := b.lockCoordinator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	release, err = reopened.lockCoordinator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := b.lockCoordinator(ctx); err == nil {
		t.Fatal("closed store acquired coordination lock")
	}
}

type failingLaunchWorkers struct {
	fakeWorkers
	failure error
}

func (f *failingLaunchWorkers) Launch(context.Context, WorkerConfig, Operation) error {
	f.launches++
	return f.failure
}

func TestCoordinatorReleasesLockOnLaunchFailureWithoutReplay(t *testing.T) {
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
	failure := errors.New("ambiguous helper creation")
	f := &failingLaunchWorkers{fakeWorkers: fakeWorkers{missing: true}, failure: failure}
	if err := (&Coordinator{Store: a, Workers: f}).Tick(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := (&Coordinator{Store: b, Workers: f}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get(first.ID)
	if err != nil || got.State != "error" || !got.NeedsAttention {
		t.Fatalf("uncertain outcome: %+v, %v", got, err)
	}
	next, err := b.Get(follower.ID)
	if err != nil || next.State != "queued" || f.launches != 1 {
		t.Fatalf("uncertain work replayed: %+v, %v, launches=%d", next, err, f.launches)
	}
}

// This child really owns the SQLite reservation in a separate OS process when
// the parent kills it. No cleanup, transaction rollback, or Store.Close runs.
type crashingLaunchWorkers struct{ fakeWorkers }

func (*crashingLaunchWorkers) Launch(context.Context, WorkerConfig, Operation) error {
	fmt.Println("COORDINATOR_LOCK_HELD")
	for {
		time.Sleep(time.Hour)
	}
}

func TestCoordinatorLockRecoversAfterProcessDeathWithoutReplay(t *testing.T) {
	const childPathEnv = "SERVICES_TEST_COORDINATOR_CRASH_PATH"
	if path := os.Getenv(childPathEnv); path != "" {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		f := &crashingLaunchWorkers{fakeWorkers: fakeWorkers{missing: true}}
		if err := (&Coordinator{Store: s, Workers: f}).Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "operations.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.Submit("first", "update project", "", "")
	if err != nil {
		t.Fatal(err)
	}
	follower, err := s.Submit("second", "update project", "", "")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^TestCoordinatorLockRecoversAfterProcessDeathWithoutReplay$", "-test.v")
	child.Env = append(os.Environ(), childPathEnv+"="+path)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			child.Process.Kill()
			child.Wait()
		}
	}()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "COORDINATOR_LOCK_HELD" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("child exited before acquiring lock")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child never reached claimed launch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	f := &fakeWorkers{missing: true}
	err = (&Coordinator{Store: s, Workers: f}).Tick(ctx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("another process bypassed lock: %v", err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("child unexpectedly exited successfully")
	}
	waited = true
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := (&Coordinator{Store: reopened, Workers: f}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(first.ID)
	if err != nil || got.State != "error" || !got.NeedsAttention {
		t.Fatalf("crash outcome: %+v, %v", got, err)
	}
	next, err := reopened.Get(follower.ID)
	if err != nil || next.State != "queued" || f.launches != 0 {
		t.Fatalf("crashed work replayed: %+v, %v, launches=%d", next, err, f.launches)
	}
}
