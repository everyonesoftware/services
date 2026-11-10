//go:build windows

package icons

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPublicationUsesWriteThroughAndFailsClosed(t *testing.T) {
	data := tinyPNG(t)
	s, err := OpenWithDownloader(t.TempDir(), downloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	original := moveFileEx
	t.Cleanup(func() { moveFileEx = original })
	var calls []string
	failMetadata := false
	sentinel := errors.New("write-through move failed")
	moveFileEx = func(from, to *uint16, flags uint32) error {
		path := windows.UTF16PtrToString(to)
		calls = append(calls, path)
		if flags != windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH {
			t.Fatalf("non-durable publication flags: %#x", flags)
		}
		if failMetadata && filepath.Base(path) == "metadata.json" {
			return sentinel
		}
		return original(from, to, flags)
	}
	if _, err := s.Put(context.Background(), "project", "p", "https://example.com/old"); err != nil {
		t.Fatal(err)
	}
	before := s.List()
	failMetadata = true
	if _, err := s.Put(context.Background(), "project", "p", "https://example.com/new"); !errors.Is(err, sentinel) {
		t.Fatalf("failed write-through move ignored: %v", err)
	}
	if len(calls) != 3 {
		t.Fatalf("unexpected fallback or skipped write-through: %v", calls)
	}
	if got := s.List(); len(got) != 1 || got[0] != before[0] {
		t.Fatal("failed move changed prior record")
	}
}

func TestWindowsInitializationUsesWriteThroughDirectoryMoves(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "nested", "store")
	original := moveFileEx
	t.Cleanup(func() { moveFileEx = original })
	var calls []string
	moveFileEx = func(from, to *uint16, flags uint32) error {
		path := windows.UTF16PtrToString(to)
		calls = append(calls, path)
		if flags != windows.MOVEFILE_WRITE_THROUGH {
			t.Fatalf("directory move flags: %#x", flags)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("directory not published via move: %v", err)
		}
		return original(from, to, flags)
	}
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0] != filepath.Dir(dir) || calls[1] != dir || calls[2] != filepath.Join(dir, "images") {
		t.Fatalf("directory durability order: %v", calls)
	}
	sentinel := errors.New("directory write-through failed")
	moveFileEx = func(*uint16, *uint16, uint32) error { return sentinel }
	failed := filepath.Join(base, "failed")
	if s, err := Open(failed); !errors.Is(err, sentinel) || s != nil {
		t.Fatalf("initialization ignored durability failure: %v", err)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "nested" {
			t.Fatalf("failed initialization left %s", entry.Name())
		}
	}
}
