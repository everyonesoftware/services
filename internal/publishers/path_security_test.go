package publishers

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStorageRejectsSymlinkPaths(t *testing.T) {
	for _, target := range []string{"directory", "ancestor", "publishers.key", "publishers.db", "publishers.db-journal", "publishers.db-wal", "publishers.db-shm"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "state")
			outside := filepath.Join(root, "outside")
			if err := os.MkdirAll(outside, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			var path, destination string
			switch target {
			case "directory":
				os.Remove(dir)
				path, destination = dir, outside
			case "ancestor":
				path, destination = filepath.Join(root, "link"), outside
				dir = filepath.Join(path, "nested")
			default:
				path, destination = filepath.Join(dir, target), filepath.Join(outside, "untouched")
				if err := os.WriteFile(destination, []byte("fixture-do-not-modify"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(destination, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			store, err := Open(dir)
			if store != nil {
				store.Close()
			}
			if err == nil {
				t.Fatal("symlink accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if target != "directory" && target != "ancestor" {
				want = 1
			}
			if len(entries) != want {
				t.Fatal("wrote outside storage directory")
			}
			if want == 1 {
				raw, err := os.ReadFile(destination)
				if err != nil || string(raw) != "fixture-do-not-modify" {
					t.Fatal("modified symlink target")
				}
			}
		})
	}
}

func TestStoragePreservesExistingDirectoryAndRestrictsPrivateFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0777 {
		t.Fatal("operator-managed directory permissions were changed")
	}
	for _, path := range []string{filepath.Join(dir, "publishers.key"), filepath.Join(dir, "publishers.db")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("storage permissions too broad: %s %v", filepath.Base(path), info.Mode())
		}
	}
}
