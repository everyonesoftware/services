package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"services/internal/auth"
)

func securedServer(t *testing.T) (*Server, *auth.Store) {
	t.Helper()
	store, err := auth.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s := New(nil, 1)
	if err := s.ConfigureAuthentication(store, "test-bootstrap-token-that-is-at-least-32-bytes"); err != nil {
		t.Fatal(err)
	}
	return s, store
}
func authRequest(s *Server, method, path string, body any, cookie *http.Cookie, token string) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&b).Encode(body)
	}
	r := httptest.NewRequest(method, "http://localhost"+path, &b)
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Del("Origin")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestAuthenticationProtectsEveryAPIRouteBeforeSetup(t *testing.T) {
	s, _ := securedServer(t)
	for _, path := range []string{"/api/projects", "/api/monitor", "/api/operations", "/api/icons", "/api/version", "/api/containers/id/update", "/api/projects/site/update", "/api/updates/check", "/api/auth/users", "/api/auth/keys", "/api/unknown"} {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
			w := authRequest(s, method, path, nil, nil, "")
			if w.Code != 401 {
				t.Errorf("%s %s = %d, want 401", method, path, w.Code)
			}
		}
	}
}
func TestFirstAdminRequiresLocalSetupTokenThenSession(t *testing.T) {
	s, _ := securedServer(t)
	body := map[string]string{"username": "admin", "password": "a sufficiently long password", "setupToken": "wrong"}
	if w := authRequest(s, "POST", "/api/auth/setup", body, nil, ""); w.Code != 403 {
		t.Fatalf("wrong setup token = %d", w.Code)
	}
	body["setupToken"] = "test-bootstrap-token-that-is-at-least-32-bytes"
	w := authRequest(s, "POST", "/api/auth/setup", body, nil, "")
	if w.Code != 201 {
		t.Fatalf("setup = %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe session cookie: %#v", cookies)
	}
	cookie := cookies[0]
	if got := authRequest(s, "GET", "/api/version", nil, cookie, ""); got.Code != 200 {
		t.Fatalf("admin access=%d", got.Code)
	}
	if got := authRequest(s, "POST", "/api/auth/setup", body, nil, ""); got.Code != 409 {
		t.Fatalf("repeat setup=%d", got.Code)
	}
	if got := authRequest(s, "POST", "/api/auth/logout", nil, cookie, ""); got.Code != 204 {
		t.Fatalf("logout=%d", got.Code)
	}
	if got := authRequest(s, "GET", "/api/version", nil, cookie, ""); got.Code != 401 {
		t.Fatalf("revoked session=%d", got.Code)
	}
}
func TestReadKeyCannotMutateOrManageCredentials(t *testing.T) {
	s, store := securedServer(t)
	_, token, err := store.CreateKey("reader", []string{"services:read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := authRequest(s, "GET", "/api/version", nil, nil, token); w.Code != 200 {
		t.Fatalf("read=%d", w.Code)
	}
	for _, path := range []string{"/api/auth/users", "/api/auth/keys", "/api/unknown"} {
		if w := authRequest(s, "GET", path, nil, nil, token); w.Code != 403 {
			t.Errorf("key GET %s=%d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/updates/check", "/api/containers/id/update", "/api/images/published", "/api/auth/keys"} {
		if w := authRequest(s, "POST", path, nil, nil, token); w.Code != 403 {
			t.Errorf("key POST %s=%d", path, w.Code)
		}
	}
}
func TestPublisherKeyCannotReadDashboardOrMutate(t *testing.T) {
	s, store := securedServer(t)
	_, token, err := store.CreateKey("publisher", []string{"publications:write"}, []string{"ghcr.io/example/app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/projects", "/api/monitor", "/api/operations", "/api/auth/keys", "/api/icons/images/example.png"} {
		if w := authRequest(s, "GET", path, nil, nil, token); w.Code != 403 {
			t.Errorf("publisher GET %s=%d", path, w.Code)
		}
	}
	if w := authRequest(s, "POST", "/api/containers/id/update", nil, nil, token); w.Code != 403 {
		t.Fatalf("publisher mutation=%d", w.Code)
	}
}
func TestSessionMutationsRequireSameOrigin(t *testing.T) {
	s, store := securedServer(t)
	if _, err := store.Bootstrap("admin", "a sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	_, token, err := store.Login("admin", "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost/api/auth/keys", bytes.NewBufferString(`{"name":"evil","scopes":["services:read"]}`))
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("CSRF=%d", w.Code)
	}
	keys, err := store.ListKeys()
	if err != nil || len(keys) != 0 {
		t.Fatalf("CSRF created keys: %v %v", keys, err)
	}
}
