package svgicon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == WorkerFlag {
		if err := RunWorker(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "--sleep-svg-test" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if len(os.Args) == 2 && os.Args[1] == "--overflow-svg-test" {
		_, _ = io.Copy(os.Stdout, io.LimitReader(zeroReader{}, MaxPNGBytes+1))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestLimitedOutputBoundsIOCopyWithoutPromotedReadFrom(t *testing.T) {
	var out limitedOutput
	_, err := io.Copy(&out, io.LimitReader(zeroReader{}, MaxPNGBytes+1))
	if err == nil || len(out.Bytes()) > MaxPNGBytes {
		t.Fatalf("output limit bypassed: bytes=%d error=%v", len(out.Bytes()), err)
	}
}

func TestNormalizeReexec(t *testing.T) {
	out, err := Normalize(context.Background(), []byte(logo))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(out, []byte("\x89PNG")) {
		t.Fatal("not PNG")
	}
}
func TestProcessDeadlineCancellationAndOutputLimit(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--sleep-svg-test", "--overflow-svg-test"} {
		r := processRenderer{slots: make(chan struct{}, 1), timeout: 100 * time.Millisecond, command: func(ctx context.Context) *exec.Cmd { return exec.CommandContext(ctx, executable, flag) }}
		start := time.Now()
		if _, err := r.render(context.Background(), []byte(logo)); err == nil {
			t.Fatal("accepted helper failure")
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("timeout did not kill child")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Normalize(ctx, []byte(logo)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
func TestProcessConcurrencyGate(t *testing.T) {
	executable, _ := os.Executable()
	started := make(chan struct{}, 8)
	r := processRenderer{slots: make(chan struct{}, 2), timeout: time.Second, command: func(ctx context.Context) *exec.Cmd {
		started <- struct{}{}
		return exec.CommandContext(ctx, executable, "--sleep-svg-test")
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = r.render(ctx, []byte(logo)) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("worker not started")
		}
	}
	select {
	case <-started:
		t.Fatal("more than two child workers")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	wg.Wait()
	if len(r.slots) != 0 {
		t.Fatal("leaked semaphore")
	}
}
