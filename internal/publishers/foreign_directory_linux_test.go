//go:build linux

package publishers

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

// Exercise the actual Linux ownership boundary: the runtime UID can write via
// its group but cannot chmod the operator-owned data mount.
func TestOpenWritableDirectoryOwnedByAnotherUser(t *testing.T) {
	if directory := os.Getenv("SERVICES_TEST_FOREIGN_DIRECTORY"); directory != "" {
		if err := os.Chmod(directory, 0770); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("fixture must deny chmod: %v", err)
		}
		probe, err := os.CreateTemp(directory, "write-probe-")
		if err != nil {
			t.Fatalf("fixture must permit writes: %v", err)
		}
		if err = probe.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(probe.Name()); err != nil {
			t.Fatal(err)
		}
		store, err := Open(directory)
		if err != nil {
			t.Fatalf("writable operator-owned data mount rejected: %v", err)
		}
		state := Snapshot{App: App{ID: 42, Slug: "fixture", PEM: "encrypted-fixture-marker"}}
		if err = store.Save(state); err != nil {
			t.Fatal(err)
		}
		if err = store.Close(); err != nil {
			t.Fatal(err)
		}
		store, err = Open(directory)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer store.Close()
		restored, err := store.Load()
		if err != nil || restored.App.PEM != state.App.PEM {
			t.Fatalf("state did not survive reopen: %v", err)
		}
		for _, name := range []string{"publishers.key", "publishers.db"} {
			info, err := os.Stat(filepath.Join(directory, name))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("private file mode weakened: %s %v", name, info.Mode())
			}
		}
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root only to construct a foreign-owned fixture and drop privileges")
	}
	root := t.TempDir()
	// Go's test-specific ancestor must also be traversable by the child UID.
	for _, path := range []string{filepath.Dir(root), root} {
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	directory := filepath.Join(root, "data")
	if err := os.Mkdir(directory, 0770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0770); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "ownership-test")
	source, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(binary, source, 0755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-test.run=^TestOpenWritableDirectoryOwnedByAnotherUser$", "-test.v")
	command.Env = append(os.Environ(), "SERVICES_TEST_FOREIGN_DIRECTORY="+directory)
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 0}}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("non-root startup: %v\n%s", err, output)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0770 {
		t.Fatal("operator-owned directory permissions were changed")
	}
}
