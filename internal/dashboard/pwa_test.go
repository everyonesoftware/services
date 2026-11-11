package dashboard

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPWAStaticResponsesRevalidate(t *testing.T) {
	// Chdir is restored by testing and cannot run alongside parallel tests.
	// This fixture never depends on the frontend build in web/dist.
	dir := t.TempDir()
	dist := filepath.Join(dir, "web", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "sw.js", "manifest.webmanifest", "offline.html"} {
		if err := os.WriteFile(filepath.Join(dist, name), []byte("fixture: "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	s, _ := securedServer(t)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range []string{"/", "/index.html", "/sw.js", "/manifest.webmanifest", "/offline.html", "/sw.js?version=2"} {
			t.Run(method+" "+path, func(t *testing.T) {
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest(method, path, nil))
				wantStatus := http.StatusOK
				if path == "/index.html" {
					wantStatus = http.StatusMovedPermanently
				}
				if w.Code != wantStatus {
					t.Errorf("status = %d, want %d", w.Code, wantStatus)
				}
				if got := w.Header().Get("Cache-Control"); got != "no-cache" && got != "no-store" {
					t.Errorf("Cache-Control = %q, want no-cache or no-store", got)
				}
				if method == http.MethodHead && w.Body.Len() != 0 {
					t.Errorf("HEAD returned a body: %q", w.Body.String())
				}
				if method == http.MethodGet && w.Code == http.StatusOK {
					name := path[1:]
					if path == "/" {
						name = "index.html"
					} else if path == "/sw.js?version=2" {
						name = "sw.js"
					}
					if got := w.Body.String(); got != "fixture: "+name {
						t.Errorf("body = %q, want static fixture for %s", got, name)
					}
				}
			})
		}
	}
}

func TestPWACachePolicyLeavesOtherStaticPathsUnchanged(t *testing.T) {
	dir := t.TempDir()
	paths := []string{"/assets/app.js", "/nested/sw.js", "/sw.js.map", "/manifest.webmanifest.bak", "/offline.html.bak"}
	for _, path := range paths {
		filename := filepath.Join(dir, "web", "dist", filepath.FromSlash(path[1:]))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte("static fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	s := New(nil, 1)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range paths {
			t.Run(method+" "+path, func(t *testing.T) {
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest(method, path, nil))
				if w.Code != http.StatusOK {
					t.Errorf("status = %d, want 200", w.Code)
				}
				if got := w.Header().Get("Cache-Control"); got != "" {
					t.Errorf("Cache-Control = %q, want unchanged empty policy", got)
				}
			})
		}
	}
}

func TestPWACachePolicyPreservesAPIAuthentication(t *testing.T) {
	s, _ := securedServer(t)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range []string{"/api/projects", "/api/version", "/api/icons/images/sw.js", "/api/unknown", "/api/projects?asset=/sw.js"} {
			t.Run(method+" "+path, func(t *testing.T) {
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest(method, path, nil))
				if w.Code != http.StatusUnauthorized {
					t.Errorf("status = %d, want 401", w.Code)
				}
				if got := w.Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("Cache-Control = %q, want existing API no-store policy", got)
				}
			})
		}
	}
	// Servers without authentication retain their existing API header behavior too.
	unsecured := New(nil, 1)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := httptest.NewRecorder()
		unsecured.ServeHTTP(w, httptest.NewRequest(method, "/api/version", nil))
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "" {
			t.Errorf("%s /api/version: status = %d, Cache-Control = %q", method, w.Code, w.Header().Get("Cache-Control"))
		}
	}
}
