package icons

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestStoreRejectsInvalidSourceEvenWithInjectedDownloader(t *testing.T) {
	calls := 0
	s, err := OpenWithDownloader(t.TempDir(), downloadFunc(func(context.Context, string) ([]byte, error) { calls++; return tinyPNG(t), nil }))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"file:///private", "http://127.0.0.1/x", "http://user:pass@example.com/x", "https://example.com/" + strings.Repeat("x", 4096)} {
		if _, err = s.Put(context.Background(), "project", "p", u); err == nil {
			t.Fatalf("accepted invalid source %s", u)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid source downloaded %d times", calls)
	}
}
func TestStoreNormalizesSupportedImagesAndRejectsUnsafeData(t *testing.T) {
	var pngData, jpegData, gifData bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 3, 2))
	_ = png.Encode(&pngData, im)
	_ = jpeg.Encode(&jpegData, im, nil)
	_ = gif.Encode(&gifData, im, nil)
	var wide, pixels bytes.Buffer
	_ = png.Encode(&wide, image.NewGray(image.Rect(0, 0, 4097, 1)))
	_ = png.Encode(&pixels, image.NewGray(image.Rect(0, 0, 2001, 2000)))
	for _, tc := range []struct {
		name  string
		data  []byte
		valid bool
	}{{"png", pngData.Bytes(), true}, {"jpeg", jpegData.Bytes(), true}, {"gif", gifData.Bytes(), true}, {"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), false}, {"html", []byte("<html>error</html>"), false}, {"large bytes", bytes.Repeat([]byte{0}, MaxDownloadBytes+1), false}, {"wide", wide.Bytes(), false}, {"too many pixels", pixels.Bytes(), false}, {"corrupt", pngData.Bytes()[:len(pngData.Bytes())-8], false}} {
		t.Run(tc.name, func(t *testing.T) {
			data := tinyPNG(t)
			s, err := OpenWithDownloader(t.TempDir(), downloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Put(context.Background(), "container", "app-1", "https://example.com/old")
			if err != nil {
				t.Fatal(err)
			}
			before := s.List()
			data = tc.data
			icon, err := s.Put(context.Background(), "container", "app-1", "https://example.com/new")
			if !tc.valid {
				if err == nil {
					t.Fatal("unsafe image accepted")
				}
				if !reflect.DeepEqual(s.List(), before) {
					t.Fatal("failure replaced prior icon")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f, err := s.Image(filepath.Base(icon.URL))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			out, err := png.Decode(f)
			if err != nil || out.Bounds().Dx() != 3 || out.Bounds().Dy() != 2 {
				t.Fatalf("normalized PNG %v", err)
			}
		})
	}
}
func TestStoreRejectsTraversalAndTamperedMetadata(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../metadata.json", "..\\metadata.json", "/etc/passwd", strings.Repeat("a", 64) + ".png/extra", "%2e%2e%2fmetadata.json", "abc.png"} {
		if f, err := s.Image(name); err == nil {
			f.Close()
			t.Fatalf("opened traversal %q", name)
		}
	}
	for _, icon := range []Icon{{Kind: "project", Key: "p", URL: "/api/icons/images/../../secret", SourceURL: "https://example.com/x"}, {Kind: "project", Key: "p", URL: "/api/icons/images/" + strings.Repeat("a", 64) + ".png", SourceURL: "http://127.0.0.1/x"}} {
		b, _ := json.Marshal(metadata{Version: 1, Icons: []Icon{icon}})
		if err = os.WriteFile(filepath.Join(dir, "metadata.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Open(dir); err == nil {
			t.Fatalf("accepted tampered metadata %+v", icon)
		}
	}
}
func TestStoreConcurrentIndependentWritesPersist(t *testing.T) {
	dir := t.TempDir()
	data := tinyPNG(t)
	s, err := OpenWithDownloader(dir, downloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, key := range []string{"one", "two", "three", "four"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			if _, err := s.Put(context.Background(), "container", key, "https://example.com/x"); err != nil {
				t.Error(err)
			}
		}(key)
	}
	wg.Wait()
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.List()) != 4 {
		t.Fatalf("lost concurrent writes: %+v", again.List())
	}
}
