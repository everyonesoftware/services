package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"services/internal/groups"
	"services/internal/icons"
)

type countedIconUpload struct {
	io.Reader
	consumed int
}

func (r *countedIconUpload) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.consumed += n
	return n, err
}

func TestIconUploadChecksInventoryBeforeReadingAndBoundsConsumedBytes(t *testing.T) {
	store, err := icons.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := &iconSource{err: errors.New("inventory offline")}
	server := New(source, 0)
	server.SetIconStore(store)
	body := &countedIconUpload{Reader: bytes.NewReader(bytes.Repeat([]byte{1}, icons.MaxDownloadBytes*2))}
	request := httptest.NewRequest("PUT", "http://localhost/api/icons/upload?kind=project&key=app", body)
	request.Header.Set("Origin", "http://localhost")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 503 || body.consumed != 0 {
		t.Fatalf("inventory failure read body: status=%d bytes=%d", response.Code, body.consumed)
	}
	source.err = nil
	source.containers = []groups.Container{{Name: "app-1", Project: "app"}}
	request = httptest.NewRequest("PUT", "http://localhost/api/icons/upload?kind=project&key=app", body)
	request.ContentLength = -1
	request.Header.Set("Origin", "http://localhost")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 413 || body.consumed != icons.MaxDownloadBytes+1 {
		t.Fatalf("unbounded upload read: status=%d bytes=%d", response.Code, body.consumed)
	}
}

func TestIconUploadRejectsUnsafeRequestsWithoutChangingSavedIcons(t *testing.T) {
	store, err := icons.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upload(context.Background(), "project", "app", iconFixture(t)); err != nil {
		t.Fatal(err)
	}
	before := store.List()
	server := New(fakeSource{containers: []groups.Container{{Name: "app-1", Project: "app"}}}, 0)
	server.SetIconStore(store)
	for _, tc := range []struct {
		name, origin, query string
		data                []byte
		status              int
	}{
		{"cross origin", "https://evil.example", "kind=project&key=app", iconFixture(t), 403},
		{"missing origin", "", "kind=project&key=app", iconFixture(t), 403},
		{"missing target", "http://localhost", "kind=project&key=missing", iconFixture(t), 404},
		{"invalid kind", "http://localhost", "kind=file&key=app", iconFixture(t), 400},
		{"duplicate target", "http://localhost", "kind=project&kind=container&key=app", iconFixture(t), 400},
		{"malformed query", "http://localhost", "kind=project&key=%xy", iconFixture(t), 400},
		{"empty", "http://localhost", "kind=project&key=app", nil, 400},
		{"html with image mime", "http://localhost", "kind=project&key=app", []byte("<html>not an image</html>"), 400},
		{"svg", "http://localhost", "kind=project&key=app", []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>"), 400},
		{"oversized streaming body", "http://localhost", "kind=project&key=app", bytes.Repeat([]byte{1}, icons.MaxDownloadBytes+1), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("PUT", "http://localhost/api/icons/upload?"+tc.query, bytes.NewReader(tc.data))
			request.ContentLength = -1
			request.Header.Set("Origin", tc.origin)
			request.Header.Set("Content-Type", "image/png")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("%d: %s", response.Code, response.Body.String())
			}
			if !reflect.DeepEqual(store.List(), before) {
				t.Fatal("failed upload changed existing icons")
			}
		})
	}
}

func TestIconUploadNormalizesRasterFormatsAndChecksDimensions(t *testing.T) {
	store, err := icons.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := New(fakeSource{containers: []groups.Container{{Name: "app-1", Project: "app"}}}, 0)
	server.SetIconStore(store)
	var jpegData, gifData, tooWide, tooMany bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, 3, 2))
	if err := jpeg.Encode(&jpegData, im, nil); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gifData, im, nil); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&tooWide, image.NewGray(image.Rect(0, 0, 4097, 1))); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&tooMany, image.NewGray(image.Rect(0, 0, 2001, 2000))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		data   []byte
		status int
	}{
		{"jpeg", jpegData.Bytes(), 200}, {"gif", gifData.Bytes(), 200}, {"dimension", tooWide.Bytes(), 400}, {"pixels", tooMany.Bytes(), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := store.List()
			request := httptest.NewRequest("PUT", "http://localhost/api/icons/upload?kind=container&key=app-1", bytes.NewReader(tc.data))
			request.Header.Set("Origin", "http://localhost")
			request.Header.Set("Content-Type", "application/octet-stream")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
			if tc.status != 200 {
				if !reflect.DeepEqual(before, store.List()) {
					t.Fatal("invalid dimensions replaced image")
				}
				return
			}
			var record icons.Icon
			if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			served := iconRequest(server, "GET", record.URL, "")
			if _, err := png.Decode(served.Body); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type brokenIconUpload struct{}

func (brokenIconUpload) Read([]byte) (int, error) { return 0, errors.New("connection failed") }
func (brokenIconUpload) Close() error             { return nil }

func TestIconUploadReadFailureAndUnavailableStorage(t *testing.T) {
	server := New(fakeSource{containers: []groups.Container{{Project: "app", Name: "app-1"}}}, 0)
	if response := iconRequest(server, "PUT", "/api/icons/upload?kind=project&key=app", ""); response.Code != 503 {
		t.Fatal(response.Code)
	}
	directory := t.TempDir()
	store, err := icons.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	server.SetIconStore(store)
	if _, err = store.Upload(context.Background(), "project", "app", iconFixture(t)); err != nil {
		t.Fatal(err)
	}
	before := store.List()
	request := httptest.NewRequest("PUT", "http://localhost/api/icons/upload?kind=project&key=app", nil)
	request.Header.Set("Origin", "http://localhost")
	request.Body = io.ReadCloser(brokenIconUpload{})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 400 || !reflect.DeepEqual(before, store.List()) {
		t.Fatal("read failure changed icon")
	}
	if err = os.Remove(filepath.Join(directory, "metadata.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(directory, "metadata.json"), 0700); err != nil {
		t.Fatal(err)
	}
	response = iconRequest(server, "PUT", "/api/icons/upload?kind=project&key=app", string(iconFixture(t)))
	if response.Code != 503 || store.Available() || !reflect.DeepEqual(before, store.List()) {
		t.Fatalf("upload did not fail closed: %d", response.Code)
	}
	response = iconRequest(server, "PUT", "/api/icons/upload?kind=project&key=app", string(iconFixture(t)))
	if response.Code != 503 {
		t.Fatal(response.Code)
	}
}

func TestIconUploadStoresValidatedLocalImageWithoutDownloading(t *testing.T) {
	directory := t.TempDir()
	downloads := 0
	store, err := icons.OpenWithDownloader(directory, iconDownloadFunc(func(context.Context, string) ([]byte, error) { downloads++; return iconFixture(t), nil }))
	if err != nil {
		t.Fatal(err)
	}
	server := New(fakeSource{containers: []groups.Container{{ID: "old", Name: "app-1", Project: "app"}}}, 0)
	server.SetIconStore(store)
	for _, target := range []string{"kind=project&key=app", "kind=container&key=app-1"} {
		request := httptest.NewRequest("PUT", "http://localhost/api/icons/upload?"+target, bytes.NewReader(iconFixture(t)))
		request.Header.Set("Origin", "http://localhost")
		request.Header.Set("Content-Type", "image/png")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("upload: %d %s", response.Code, response.Body.String())
		}
		var record icons.Icon
		if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if record.SourceURL != "" || record.URL == "" {
			t.Fatalf("uploaded image provenance: %+v", record)
		}
		image := iconRequest(server, "GET", record.URL, "")
		if image.Code != 200 {
			t.Fatal(image.Code)
		}
		if _, err := png.Decode(image.Body); err != nil {
			t.Fatal(err)
		}
	}
	if downloads != 0 {
		t.Fatalf("upload performed %d remote downloads", downloads)
	}
	reopened, err := icons.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.List()) != 2 || !reflect.DeepEqual(store.List(), reopened.List()) {
		t.Fatal("uploaded images did not persist independently")
	}
}
