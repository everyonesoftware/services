//go:build !windows

package icons

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDirectorySyncOrderingAndFailures(t *testing.T) {
	data := tinyPNG(t)
	dir := t.TempDir()
	s, err := OpenWithDownloader(dir, downloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	original := syncDirectory
	t.Cleanup(func() { syncDirectory = original })
	images := filepath.Join(dir, "images")
	failAt := images
	sentinel := errors.New("fsync failed")
	var calls []string
	syncDirectory = func(path string) error {
		calls = append(calls, path)
		if path == images {
			files, err := os.ReadDir(images)
			if err != nil || len(files) != 1 || !assetName.MatchString(files[0].Name()) {
				t.Fatalf("sync before image rename: %v, %v", files, err)
			}
			if len(s.records) != 0 {
				t.Fatal("metadata exposed before image sync")
			}
		}
		if path == failAt {
			return sentinel
		}
		return original(path)
	}
	// First attempt leaves a renamed-but-unsynced image. Retrying must sync
	// that existing entry too, not skip directly to metadata publication.
	for i := 0; i < 2; i++ {
		if _, err := s.Put(context.Background(), "project", "p", "https://example.com/new"); !errors.Is(err, sentinel) {
			t.Fatalf("attempt %d bypassed image fsync: %v", i, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "metadata.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("metadata published before image sync: %v", err)
		}
	}
	failAt = dir
	if _, err := s.Put(context.Background(), "project", "p", "https://example.com/new"); !errors.Is(err, sentinel) {
		t.Fatalf("metadata fsync failure ignored: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatal("metadata fsync failure reported success in memory")
	}
	failAt = ""
	if _, err := s.Put(context.Background(), "project", "p", "https://example.com/new"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("metadata fsync failure did not disable subsequent mutation: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{images, images, images, dir}) {
		t.Fatalf("sync ordering: %v", calls)
	}
	syncDirectory = original
	recovered, err := OpenWithDownloader(dir, s.downloader)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered.List()) != 1 {
		t.Fatal("reopen did not recover renamed metadata")
	}
	if _, err := recovered.Put(context.Background(), "project", "p", "https://example.com/new"); err != nil {
		t.Fatal(err)
	}
}

func TestInitializationPersistsDirectoriesBeforeChildren(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "new", "store")
	original := syncDirectory
	t.Cleanup(func() { syncDirectory = original })
	var calls []string
	syncDirectory = func(path string) error {
		if path == base || path == filepath.Dir(dir) || path == dir {
			calls = append(calls, path)
			if _, err := os.Stat(filepath.Join(path, "images")); err == nil && path != dir {
				t.Fatal("unexpected child publication")
			}
		}
		return original(path)
	}
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{base, filepath.Dir(dir), dir}) {
		t.Fatalf("directory creation durability order: %v", calls)
	}
	sentinel := errors.New("initialization sync failed")
	syncDirectory = func(path string) error {
		if path == base {
			return sentinel
		}
		return original(path)
	}
	if s, err := Open(filepath.Join(base, "another", "store")); !errors.Is(err, sentinel) || s != nil {
		t.Fatalf("initialization ignored fsync failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "another", "store")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("children created before parent durability: %v", err)
	}
}
