package icons

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestOpenWritableDirectoriesLeavesNoProbes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "store")
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, filepath.Join(dir, "images")} {
		entries, err := os.ReadDir(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if path != dir || entry.Name() != "images" {
				t.Fatalf("initialization left %s", filepath.Join(path, entry.Name()))
			}
		}
	}
}

func TestOpenRejectsExistingReadOnlyDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not enforce directory ACLs on Windows")
	}
	for _, name := range []string{"store", "images"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Open(dir); err != nil {
				t.Fatal(err)
			}
			metadata := []byte(`{"version":1,"icons":[]}`)
			if err := os.WriteFile(filepath.Join(dir, "metadata.json"), metadata, 0600); err != nil {
				t.Fatal(err)
			}
			path := dir
			if name == "images" {
				path = filepath.Join(dir, "images")
			}
			if err := os.Chmod(path, 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0700) })
			f, err := os.CreateTemp(path, ".permission-check-*")
			if err == nil {
				f.Close()
				os.Remove(f.Name())
				t.Skip("process can write despite chmod (e.g. root capabilities)")
			}
			if s, err := Open(dir); err == nil || s != nil {
				t.Fatal("read-only directory enabled the store")
			}
			got, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
			if err != nil || !reflect.DeepEqual(got, metadata) {
				t.Fatalf("existing metadata changed: %q, %v", got, err)
			}
			for _, p := range []string{dir, filepath.Join(dir, "images")} {
				entries, err := os.ReadDir(p)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if entry.Name() != "images" && entry.Name() != "metadata.json" {
						t.Fatalf("failed initialization left probe %s", entry.Name())
					}
				}
			}
		})
	}
}
