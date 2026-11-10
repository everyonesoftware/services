//go:build !windows

package publishers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStorageCreatesPrivateDirectories(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "new", "state")
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, path := range []string{filepath.Dir(directory), directory} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0700 {
			t.Fatalf("new directory must remain private: %s %v", path, info.Mode())
		}
	}
}
