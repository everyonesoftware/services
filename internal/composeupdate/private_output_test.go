package composeupdate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateConfigOutputNeverStreamsSecrets(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("executable shell fixture runs in Linux CI")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf '{\"services\":{\"secret\":\"sensitive\"}}'\nprintf 'secret stderr' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	logs := []string{}
	ctx := WithProgress(context.Background(), func(s string) { logs = append(logs, s) })
	_, err := (execRunner{dockerSocket: "/var/run/docker.sock"}).Output(ctx, dir, []string{"config", "--format", "json"})
	if err == nil || strings.Contains(err.Error(), "secret") || len(logs) != 0 {
		t.Fatalf("leaked private output: %v %v", err, logs)
	}
}
