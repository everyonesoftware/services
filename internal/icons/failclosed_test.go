package icons

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestMetadataRenameFailureBlocksMutationsUntilReopen(t *testing.T) {
	for _, mutation := range []string{"Put", "Delete"} {
		t.Run(mutation, func(t *testing.T) {
			dir := t.TempDir()
			data := tinyPNG(t)
			var downloads atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			downloader := downloadFunc(func(_ context.Context, url string) ([]byte, error) {
				downloads.Add(1)
				if url == "https://example.com/racing" {
					close(started)
					<-release
				}
				return data, nil
			})
			s, err := OpenWithDownloader(dir, downloader)
			if err != nil {
				t.Fatal(err)
			}
			old, err := s.Put(context.Background(), "project", "app", "https://example.com/old")
			if err != nil {
				t.Fatal(err)
			}
			before := s.List()
			if !s.Available() {
				t.Fatal("healthy store unavailable")
			}
			racing := make(chan error, 1)
			go func() {
				_, err := s.Put(context.Background(), "project", "racing", "https://example.com/racing")
				racing <- err
			}()
			<-started
			barrierErr := errors.New("metadata directory fsync failed after rename")
			publications := 0
			s.publish = func(from, to string) error {
				publications++
				if err := os.Rename(from, to); err != nil {
					return err
				}
				if filepath.Base(to) == "metadata.json" {
					return barrierErr
				}
				return nil
			}
			if mutation == "Put" {
				_, err = s.Put(context.Background(), "project", "app", "https://example.com/new")
			} else {
				err = s.Delete("project", "app")
			}
			if !errors.Is(err, barrierErr) {
				t.Fatalf("expected post-rename failure: %v", err)
			}
			if !errors.Is(err, ErrUnavailable) || s.Available() {
				t.Fatal("metadata failure did not disable store")
			}
			published := publications
			close(release)
			if err := <-racing; !errors.Is(err, ErrUnavailable) {
				t.Errorf("in-flight Put not disabled: %v", err)
			}
			count := downloads.Load()
			if _, err := s.Put(context.Background(), "project", "later", "https://example.com/later"); !errors.Is(err, ErrUnavailable) {
				t.Errorf("subsequent Put not disabled: %v", err)
			}
			if err := s.Delete("project", "app"); !errors.Is(err, ErrUnavailable) {
				t.Errorf("subsequent Delete not disabled: %v", err)
			}
			if publications != published {
				t.Error("disabled mutation published stale records")
			}
			if downloads.Load() != count {
				t.Error("disabled store invoked downloader")
			}
			if !reflect.DeepEqual(before, s.List()) {
				t.Error("last-known-good records changed")
			}
			f, err := s.Image(filepath.Base(old.URL))
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
			reopened, err := OpenWithDownloader(dir, downloader)
			if err != nil {
				t.Fatal(err)
			}
			if !reopened.Available() {
				t.Fatal("reopened store unavailable")
			}
			visible := reopened.List()
			if mutation == "Put" {
				if len(visible) != 1 || visible[0].SourceURL != "https://example.com/new" {
					t.Fatalf("did not recover published replacement: %+v", visible)
				}
			} else if len(visible) != 0 {
				t.Fatalf("did not recover published deletion: %+v", visible)
			}
			if _, err = reopened.Put(context.Background(), "project", "recovered", "https://example.com/recovered"); err != nil {
				t.Fatal(err)
			}
			again, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(reopened.List(), again.List()) || len(again.List()) != len(visible)+1 {
				t.Fatal("recovery lost visible metadata")
			}
		})
	}
}
