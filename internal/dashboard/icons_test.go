package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"services/internal/groups"
	"services/internal/icons"
)

type iconDownloadFunc func(context.Context, string) ([]byte, error)

func (f iconDownloadFunc) Download(c context.Context, u string) ([]byte, error) { return f(c, u) }
func iconFixture(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func iconRequest(s *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestIconsUnavailableContract(t *testing.T) {
	s := New(fakeSource{}, 1)
	w := iconRequest(s, "GET", "/api/icons", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"available":false,"icons":[]}` {
		t.Fatalf("unavailable %d %s", w.Code, w.Body.String())
	}
	for _, method := range []string{"PUT", "DELETE"} {
		w = iconRequest(s, method, "/api/icons?kind=project&key=app", `{"kind":"project","key":"app","url":"https://example.com/logo"}`)
		if w.Code != 503 {
			t.Fatalf("%s unavailable: %d", method, w.Code)
		}
	}
}
func TestIconRoutesPersistLocalImagesAndIndependentStableTargets(t *testing.T) {
	dir := t.TempDir()
	data := iconFixture(t)
	downloads := 0
	store, err := icons.OpenWithDownloader(dir, iconDownloadFunc(func(context.Context, string) ([]byte, error) { downloads++; return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	source := &iconSource{containers: []groups.Container{{ID: "old-id", Name: "app-1", Project: "app"}, {ID: "replica-id", Name: "app-2", Project: "app"}, {ID: "solo-id", Name: "solo"}}}
	updater := &fakeServiceUpdater{}
	s := NewWithActions(source, nil, updater, 1)
	s.SetIconStore(store)
	var saved icons.Icon
	for _, target := range []struct{ kind, key string }{{"project", "app"}, {"project", "Standalone"}, {"container", "app-1"}, {"container", "app-2"}} {
		body, _ := json.Marshal(map[string]string{"kind": target.kind, "key": target.key, "url": "https://example.com/logo"})
		w := iconRequest(s, "PUT", "/api/icons", string(body))
		if w.Code != 200 {
			t.Fatalf("put %s %s: %d %s", target.kind, target.key, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Kind != target.kind || saved.Key != target.key || saved.SourceURL != "https://example.com/logo" || !strings.HasPrefix(saved.URL, "/api/icons/images/") {
			t.Fatalf("record %+v", saved)
		}
	}
	source.containers[0].ID = "recreated-id"
	restarted, err := icons.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.SetIconStore(restarted)
	w := iconRequest(s, "GET", "/api/icons", "")
	var listing struct {
		Available bool         `json:"available"`
		Icons     []icons.Icon `json:"icons"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !listing.Available || len(listing.Icons) != 4 || downloads != 4 {
		t.Fatalf("list %d %+v downloads=%d", w.Code, listing, downloads)
	}
	imageResponse := iconRequest(s, "GET", saved.URL, "")
	if imageResponse.Code != 200 || imageResponse.Header().Get("Content-Type") != "image/png" || imageResponse.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(imageResponse.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("image %d %v", imageResponse.Code, imageResponse.Header())
	}
	if _, err = png.Decode(imageResponse.Body); err != nil {
		t.Fatal(err)
	}
	w = iconRequest(s, "DELETE", "/api/icons?kind=container&key="+url.QueryEscape("app-1"), "")
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("reset %d %s", w.Code, w.Body.String())
	}
	after, err := icons.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.List()) != 3 {
		t.Fatalf("reset list %+v", after.List())
	}
	for _, icon := range after.List() {
		if icon.Key == "app-1" {
			t.Fatal("reset retained icon")
		}
	}
	// Keep immutable assets alive for in-flight clients even after reset/replacement.
	asset, err := store.Image(filepath.Base(saved.URL))
	if err != nil {
		t.Fatal(err)
	}
	_ = asset.Close()
	if len(updater.pulls) != 0 || len(updater.updates) != 0 {
		t.Fatal("icon request mutated containers")
	}
}

type iconSource struct {
	containers []groups.Container
	calls      int
	err        error
}

func (s *iconSource) Containers() ([]groups.Container, error) { s.calls++; return s.containers, s.err }
