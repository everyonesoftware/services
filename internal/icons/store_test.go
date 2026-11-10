package icons

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type downloadFunc func(context.Context, string) ([]byte, error)

func (f downloadFunc) Download(ctx context.Context, u string) ([]byte, error) { return f(ctx, u) }
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 2, 3))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestStorePersistsIndependentTargetsAndReset(t *testing.T) {
	dir := t.TempDir()
	data := tinyPNG(t)
	s, err := OpenWithDownloader(dir, downloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ kind, key string }{{"project", "app"}, {"container", "app-1"}, {"container", "app-2"}} {
		icon, err := s.Put(context.Background(), target.kind, target.key, "https://example.com/logo.png")
		if err != nil {
			t.Fatal(err)
		}
		if icon.Kind != target.kind || icon.Key != target.key || icon.SourceURL != "https://example.com/logo.png" || icon.URL == icon.SourceURL {
			t.Fatalf("icon=%+v", icon)
		}
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.List(), reopened.List()) || len(reopened.List()) != 3 {
		t.Fatalf("restart icons=%+v", reopened.List())
	}
	asset, err := reopened.Image(filepath.Base(reopened.List()[0].URL))
	if err != nil {
		t.Fatal(err)
	}
	defer asset.Close()
	im, err := png.Decode(asset)
	if err != nil || im.Bounds().Dx() != 2 || im.Bounds().Dy() != 3 {
		t.Fatalf("normalized image: %v", err)
	}
	if err := reopened.Delete("container", "app-1"); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.List()) != 2 {
		t.Fatalf("reset=%+v", again.List())
	}
}
func TestStoreReusesImmutableContentAddressedAssets(t *testing.T) {
	data := tinyPNG(t)
	dir := t.TempDir()
	s, err := OpenWithDownloader(dir, downloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	icon, err := s.Put(context.Background(), "project", "one", "https://example.com/logo")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "images", filepath.Base(icon.URL))
	old := time.Unix(1_600_000_000, 0)
	if err = os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	f, err := s.Image(filepath.Base(icon.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = s.Put(context.Background(), "container", "two", "https://example.com/logo"); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !stat.ModTime().Equal(old) {
		t.Fatal("content-addressed asset was rewritten")
	}
}
func TestStoreFailurePreservesPriorRecord(t *testing.T) {
	dir := t.TempDir()
	fail := false
	data := tinyPNG(t)
	s, err := OpenWithDownloader(dir, downloadFunc(func(context.Context, string) ([]byte, error) {
		if fail {
			return nil, errors.New("offline")
		}
		return data, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Put(context.Background(), "project", "app", "https://example.com/old"); err != nil {
		t.Fatal(err)
	}
	before := s.List()
	fail = true
	if _, err = s.Put(context.Background(), "project", "app", "https://example.com/new"); err == nil {
		t.Fatal("expected failure")
	}
	if !reflect.DeepEqual(before, s.List()) {
		t.Fatal("download failure changed icon")
	}
	fail = false
	// Replacing the metadata target with a directory forces an atomic commit failure.
	if err = os.Remove(filepath.Join(dir, "metadata.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "metadata.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Put(context.Background(), "project", "app", "https://example.com/new"); err == nil {
		t.Fatal("expected persistence failure")
	}
	if !reflect.DeepEqual(before, s.List()) {
		t.Fatal("commit failure changed icon")
	}
	if err = s.Delete("project", "app"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected disabled store after persistence failure: %v", err)
	}
	if !reflect.DeepEqual(before, s.List()) {
		t.Fatal("failed delete changed icon")
	}
}
