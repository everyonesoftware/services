package composeupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerDrainsProgressBeforeClosingPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX executable like the production image")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\ni=0\nwhile [ \"$i\" -lt 100 ]; do echo \"record:$i\"; i=$((i+1)); done\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	var messages []string
	ctx := WithProgress(context.Background(), func(line string) {
		messages = append(messages, line)
		if strings.HasPrefix(line, "stdout: record:") {
			time.Sleep(time.Millisecond)
		}
	})
	if err := (execRunner{dockerSocket: "/fixture.sock"}).Run(ctx, dir, []string{"version"}); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range messages {
		if strings.Contains(line, "Output read error") {
			t.Fatalf("closed progress pipe before draining: %s", line)
		}
		if strings.HasPrefix(line, "stdout: record:") {
			count++
		}
	}
	if count != 100 {
		t.Fatalf("lost progress records: got %d, want 100", count)
	}
}

func TestExecRunnerCancellationBoundsInheritedProgressPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("production uses POSIX child processes")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nsleep 30 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (execRunner{dockerSocket: "/fixture.sock"}).Run(ctx, dir, []string{"version"}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled command succeeded")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancelled runner hung on a descendant's inherited progress pipes")
	}
}
