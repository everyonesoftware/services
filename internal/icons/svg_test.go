package icons

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"services/internal/svgicon"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == svgicon.WorkerFlag {
		if err := svgicon.RunWorker(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const svgLogo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50"><path d="M0 0 H100 V50 H0 Z" fill="#ff0000"/></svg>`

func TestStoreRejectsUnsupportedSVGOpacityPreservingRecords(t *testing.T) {
	data := []byte(svgLogo)
	s, err := OpenWithDownloader(t.TempDir(), downloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(context.Background(), "project", "p", data); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(context.Background(), "container", "c", "https://example.com/valid.svg"); err != nil {
		t.Fatal(err)
	}
	before := s.List()
	for _, body := range []string{
		`<g opacity=".5"><rect width="70" height="50" fill="red"/><rect x="30" width="70" height="50" fill="red"/></g>`,
		`<g fill-opacity=".5"><g><rect width="100" height="50" fill="red" fill-opacity=".5"/></g></g>`,
	} {
		data = []byte(strings.Replace(svgLogo, `<path d="M0 0 H100 V50 H0 Z" fill="#ff0000"/>`, body, 1))
		if _, err := s.Upload(context.Background(), "project", "p", data); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("upload accepted unsupported opacity: %v", err)
		}
		if _, err := s.Put(context.Background(), "container", "c", "https://example.com/invalid.svg"); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("download accepted unsupported opacity: %v", err)
		}
		if !reflect.DeepEqual(before, s.List()) {
			t.Fatal("rejected opacity changed stored records")
		}
	}
}

func TestStoreSVGURLAndUploadCanonicalPNG(t *testing.T) {
	data := []byte(svgLogo)
	s, err := OpenWithDownloader(t.TempDir(), downloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := s.Upload(context.Background(), "project", "p", data)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, err := s.Put(context.Background(), "container", "c", "https://example.com/not-an-image.txt")
	if err != nil {
		t.Fatal(err)
	}
	if uploaded.URL != downloaded.URL || uploaded.SourceURL != "" || downloaded.SourceURL == "" {
		t.Fatal("canonical assets/provenance mismatch")
	}
	f, err := s.Image(filepath.Base(uploaded.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	im, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if im.Bounds().Dx() != 100 || im.Bounds().Dy() != 50 {
		t.Fatal(im.Bounds())
	}
	r, _, _, a := im.At(50, 25).RGBA()
	if r == 0 || a == 0 {
		t.Fatal("missing painted shape")
	}
	before := s.List()
	for _, body := range []string{`<script/>`, `<image href="https://example.com/a.png"/>`, `<rect onload="alert(1)"/>`, `<rect fill="url(file:///private)"/>`, `<foreignObject/>`} {
		data = []byte(strings.Replace(svgLogo, `<path d="M0 0 H100 V50 H0 Z" fill="#ff0000"/>`, body, 1))
		if _, err := s.Upload(context.Background(), "project", "p", data); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("upload accepted: %v", err)
		}
		if _, err := s.Put(context.Background(), "container", "c", "https://example.com/evil.svg"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("URL accepted: %v", err)
		}
		if !reflect.DeepEqual(before, s.List()) {
			t.Fatal("invalid SVG changed records")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data = []byte(svgLogo)
	if _, err := s.Upload(ctx, "project", "p", data); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, "container", "c", "https://example.com/logo"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, s.List()) {
		t.Fatal("cancellation changed records")
	}
	s.mu.Lock()
	s.failed = ErrUnavailable
	s.mu.Unlock()
	if _, err := s.Upload(context.Background(), "project", "p", []byte(svgLogo)); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
