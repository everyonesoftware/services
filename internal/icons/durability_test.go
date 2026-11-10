package icons

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPutWaitsForDurableImageBeforeMetadata(t *testing.T) {
	data := tinyPNG(t)
	dir := t.TempDir()
	s, err := OpenWithDownloader(dir, downloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	barrierErr := errors.New("directory durability failed")
	var calls []string
	s.publish = func(from, to string) error {
		calls = append(calls, filepath.Base(filepath.Dir(to)))
		if filepath.Dir(to) == filepath.Join(dir, "images") {
			// Model failure after the rename, before its directory is durable.
			if err := os.Rename(from, to); err != nil {
				return err
			}
			return barrierErr
		}
		return os.Rename(from, to)
	}
	if _, err := s.Put(context.Background(), "project", "p", "https://example.com/new"); !errors.Is(err, barrierErr) {
		t.Fatalf("Put did not wait for image durability: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatal("image durability failure committed in-memory metadata")
	}
	if _, err := os.Stat(filepath.Join(dir, "metadata.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metadata committed ahead of image durability: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"images"}) {
		t.Fatalf("publication order: %v", calls)
	}
}

func TestPutWaitsForDurableMetadata(t *testing.T) {
	data := tinyPNG(t)
	s, err := OpenWithDownloader(t.TempDir(), downloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	barrierErr := errors.New("metadata durability failed")
	var calls []string
	s.publish = func(from, to string) error {
		calls = append(calls, filepath.Base(to))
		if filepath.Base(to) == "metadata.json" {
			return barrierErr
		}
		return os.Rename(from, to)
	}
	if _, err := s.Put(context.Background(), "project", "p", "https://example.com/new"); !errors.Is(err, barrierErr) {
		t.Fatalf("Put did not wait for metadata durability: %v", err)
	}
	if len(s.List()) != 0 {
		t.Fatal("failed metadata publication changed memory")
	}
	if len(calls) != 2 || calls[1] != "metadata.json" {
		t.Fatalf("publication order: %v", calls)
	}
}
