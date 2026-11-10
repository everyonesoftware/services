//go:build !windows

package publishers

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenFailsClosedOnDirectorySyncFailure(t *testing.T) {
	original := syncDirectory
	defer func() { syncDirectory = original }()
	failure := errors.New("fixture-directory-sync-failure")
	syncDirectory = func(string) error { return failure }
	store, err := Open(t.TempDir())
	if store != nil {
		store.Close()
	}
	if !errors.Is(err, failure) {
		t.Fatalf("directory durability failure ignored: %v", err)
	}
}

func TestExistingStorageDoesNotRequireOpeningUnchangedAncestors(t *testing.T) {
	original := syncDirectory
	defer func() { syncDirectory = original }()
	dir := t.TempDir()
	syncDirectory = func(path string) error {
		if path != dir {
			return os.ErrPermission
		}
		return original(path)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("unchanged ancestor need not be readable: %v", err)
	}
	store.Close()
}

func TestOpenSyncsKeyAndDatabaseDirectoryEntries(t *testing.T) {
	original := syncDirectory
	defer func() { syncDirectory = original }()
	root := t.TempDir()
	dir := filepath.Join(root, "nested", "state")
	sawKey, sawDatabase := false, false
	synced := map[string]bool{}
	syncDirectory = func(path string) error {
		synced[path] = true
		if path == dir {
			if info, err := os.Stat(filepath.Join(dir, "publishers.key")); err == nil && info.Size() == 32 {
				sawKey = true
			}
			if info, err := os.Stat(filepath.Join(dir, "publishers.db")); err == nil && info.Size() > 0 {
				sawDatabase = true
			}
		}
		return original(path)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if !sawKey || !sawDatabase || !synced[root] || !synced[filepath.Join(root, "nested")] {
		t.Fatal("new key, database or directory ancestors not synced")
	}
}
