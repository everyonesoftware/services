package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"services/internal/groups"
	"services/internal/icons"
)

func configuredIconServer(t *testing.T) (*Server, *iconSource, *int) {
	t.Helper()
	data := iconFixture(t)
	calls := new(int)
	store, err := icons.OpenWithDownloader(t.TempDir(), iconDownloadFunc(func(context.Context, string) ([]byte, error) { *calls++; return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	source := &iconSource{containers: []groups.Container{{ID: "container-id", Name: "api-1", Project: "app"}}}
	s := New(source, 1)
	s.SetIconStore(store)
	return s, source, calls
}
func TestIconMutationsRequireSameOriginBeforeInventoryOrDownload(t *testing.T) {
	for _, method := range []string{"PUT", "DELETE"} {
		for _, tc := range []struct{ name, host, origin, site string }{{"missing origin", "localhost", "", ""}, {"cross origin", "localhost", "http://attacker.test", ""}, {"untrusted same origin", "attacker.test", "http://attacker.test", ""}, {"cross scheme", "localhost", "https://localhost", ""}, {"cross fetch site", "localhost", "http://localhost", "cross-site"}, {"wrong port", "localhost", "http://localhost:80x", ""}} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				s, source, calls := configuredIconServer(t)
				r := httptest.NewRequest(method, "http://"+tc.host+"/api/icons?kind=project&key=app", strings.NewReader(`{"kind":"project","key":"app","url":"https://example.com/x"}`))
				if tc.origin != "" {
					r.Header.Set("Origin", tc.origin)
				}
				if tc.site != "" {
					r.Header.Set("Sec-Fetch-Site", tc.site)
				}
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if w.Code != 403 || source.calls != 0 || *calls != 0 || len(s.iconStore.List()) != 0 {
					t.Fatalf("rejected origin %d inventory=%d downloads=%d icons=%v", w.Code, source.calls, *calls, s.iconStore.List())
				}
			})
		}
	}
}
func TestIconMutationsValidateTargetBeforeDownload(t *testing.T) {
	for _, method := range []string{"PUT", "DELETE"} {
		for _, tc := range []struct {
			kind, key string
			status    int
		}{{"container", "container-id", 404}, {"container", "api", 404}, {"project", "missing", 404}, {"project", "Standalone", 404}, {"container", "missing", 404}, {"service", "api-1", 400}, {"project", "", 400}} {
			t.Run(method+"/"+tc.kind+"/"+tc.key, func(t *testing.T) {
				s, _, calls := configuredIconServer(t)
				body, _ := json.Marshal(map[string]string{"kind": tc.kind, "key": tc.key, "url": "https://example.com/x"})
				path := "/api/icons"
				if method == "DELETE" {
					path += "?kind=" + tc.kind + "&key=" + tc.key
				}
				w := iconRequest(s, method, path, string(body))
				if w.Code != tc.status || *calls != 0 || len(s.iconStore.List()) != 0 {
					t.Fatalf("target %d %s downloads=%d", w.Code, w.Body.String(), *calls)
				}
			})
		}
	}
	s, source, calls := configuredIconServer(t)
	source.err = errors.New("engine down")
	for _, method := range []string{"PUT", "DELETE"} {
		w := iconRequest(s, method, "/api/icons?kind=project&key=app", `{"kind":"project","key":"app","url":"https://example.com/x"}`)
		if w.Code != 503 || *calls != 0 {
			t.Fatalf("inventory failure %d downloads=%d", w.Code, *calls)
		}
	}
	// Read-only metadata is independent of the engine and never refreshes remotely.
	source.calls = 0
	w := iconRequest(s, "GET", "/api/icons", "")
	if w.Code != 200 || *calls != 0 || source.calls != 0 {
		t.Fatal("GET queried dependencies")
	}
}
func TestIconPutBoundsAndStrictPayload(t *testing.T) {
	for _, body := range []string{``, `null`, `[]`, `{}`, `{"kind":"project","key":"app","url":"https://example.com/x","unexpected":true}`, `{"kind":"project","key":"app","url":"https://example.com/x"} {}`, `{"kind":"project","key":"app","url":"https://example.com/x"}` + strings.Repeat(" ", 8192), `{"kind":"project","key":"app","url":"` + strings.Repeat("x", 8192) + `"}`, `{"kind":"project","key":"app","url":3}`} {
		s, _, calls := configuredIconServer(t)
		w := iconRequest(s, "PUT", "/api/icons", body)
		if w.Code != 400 && w.Code != 413 {
			t.Fatalf("invalid payload accepted: status=%d body=%s", w.Code, w.Body.String())
		}
		if *calls != 0 {
			t.Fatal("invalid payload downloaded")
		}
	}
	for _, query := range []string{"kind=project&kind=container&key=app", "kind=project&key=app&key=other", "kind=project&key=%zz"} {
		s, _, _ := configuredIconServer(t)
		w := iconRequest(s, "DELETE", "/api/icons?"+query, "")
		if w.Code != 400 {
			t.Fatalf("ambiguous delete query %q: %d", query, w.Code)
		}
	}
}
func TestIconRouteDownloadAndPersistenceFailuresPreservePrior(t *testing.T) {
	dir := t.TempDir()
	data := iconFixture(t)
	fail := false
	store, err := icons.OpenWithDownloader(dir, iconDownloadFunc(func(context.Context, string) ([]byte, error) {
		if fail {
			return nil, errors.New("offline")
		}
		return data, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	s := New(fakeSource{containers: []groups.Container{{Name: "api-1", Project: "app"}}}, 1)
	s.SetIconStore(store)
	put := func(source string) *httptest.ResponseRecorder {
		return iconRequest(s, "PUT", "/api/icons", `{"kind":"project","key":"app","url":"`+source+`"}`)
	}
	if w := put("https://example.com/old"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	before := store.List()
	fail = true
	if w := put("https://example.com/new"); w.Code != 502 {
		t.Fatalf("download failure status=%d", w.Code)
	}
	if !reflect.DeepEqual(before, store.List()) {
		t.Fatal("download failure replaced record")
	}
	reopened, err := icons.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, reopened.List()) {
		t.Fatal("download failure changed disk")
	}
	fail = false
	metadataPath := filepath.Join(dir, "metadata.json")
	if err = os.Remove(metadataPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(metadataPath, 0700); err != nil {
		t.Fatal(err)
	}
	if w := put("https://example.com/new"); w.Code != 503 {
		t.Fatalf("persistence failure status=%d", w.Code)
	}
	w := iconRequest(s, "GET", "/api/icons", "")
	var listing struct {
		Available bool         `json:"available"`
		Icons     []icons.Icon `json:"icons"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || listing.Available || !reflect.DeepEqual(before, listing.Icons) {
		t.Fatalf("failed store editing contract: %d %s", w.Code, w.Body.String())
	}
	// A disabled store must reject before even attempting another download.
	fail = true
	if w := put("https://example.com/later"); w.Code != 503 {
		t.Fatalf("disabled put: %d", w.Code)
	}
	if w := iconRequest(s, "DELETE", "/api/icons?kind=project&key=app", ""); w.Code != 503 {
		t.Fatalf("disabled delete: %d", w.Code)
	}
	if w := iconRequest(s, "GET", before[0].URL, ""); w.Code != 200 {
		t.Fatalf("last-known-good image: %d", w.Code)
	}
	if !reflect.DeepEqual(before, store.List()) {
		t.Fatal("persistence failure replaced record")
	}
}
func TestIconRouteReplacementFailuresPreserveOtherTargets(t *testing.T) {
	s, _, calls := configuredIconServer(t)
	for _, kind := range []string{"project", "container"} {
		key := "app"
		if kind == "container" {
			key = "api-1"
		}
		body, _ := json.Marshal(map[string]string{"kind": kind, "key": key, "url": "https://example.com/old"})
		if w := iconRequest(s, "PUT", "/api/icons", string(body)); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	before := s.iconStore.List()
	for _, source := range []string{"http://127.0.0.1/x", "file:///tmp/x", "https://user:pass@example.com/x", "https://example.com/" + strings.Repeat("x", 4096)} {
		body, _ := json.Marshal(map[string]string{"kind": "project", "key": "app", "url": source})
		w := iconRequest(s, "PUT", "/api/icons", string(body))
		if w.Code != 400 || !reflect.DeepEqual(before, s.iconStore.List()) || *calls != 2 {
			t.Fatalf("bad source changed records: %d %+v", w.Code, s.iconStore.List())
		}
	}
	w := iconRequest(s, "PUT", "/api/icons", `{"kind":"project","key":"app","url":"https://example.com/replacement"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	after := s.iconStore.List()
	if after[0] != before[0] || after[1].SourceURL != "https://example.com/replacement" {
		t.Fatalf("replacement independence before=%+v after=%+v", before, after)
	}
	for _, path := range []string{"/api/icons/images/metadata.json", "/api/icons/images/%2e%2e%2fmetadata.json", "/api/icons/images/%2e%2e%5cmetadata.json", "/api/icons/images/abc.png"} {
		w = iconRequest(s, "GET", path, "")
		if w.Code == 200 {
			t.Fatalf("unsafe asset served %s", path)
		}
	}
}
