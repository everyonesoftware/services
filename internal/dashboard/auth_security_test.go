package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagedPublisherKeysAreRepositoryScopedAndRevocable(t *testing.T) {
	s, store := securedServer(t)
	receiver := &publicationReceiver{}
	if err := s.ConfigurePublicationReceiver(receiver); err != nil {
		t.Fatal(err)
	}
	key, token, err := store.CreateKey("ci", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	body := publicationBody(t)
	if w := publicationRequest(t, s, body, token); w.Code != 200 || receiver.calls != 1 {
		t.Fatalf("ingest=%d calls=%d %s", w.Code, receiver.calls, w.Body.String())
	}
	other := strings.Replace(body, `"repository":"ghcr.io/acme/app"`, `"repository":"ghcr.io/acme/other"`, 1)
	if w := publicationRequest(t, s, other, token); w.Code != 403 || receiver.calls != 1 {
		t.Fatalf("scope=%d calls=%d", w.Code, receiver.calls)
	}
	if err := store.RevokeKey(key.ID); err != nil {
		t.Fatal(err)
	}
	if w := publicationRequest(t, s, body, token); w.Code != 401 || receiver.calls != 1 {
		t.Fatalf("revoked=%d calls=%d", w.Code, receiver.calls)
	}
}
func TestLegacyPublisherCannotBypassApplicationAuthorization(t *testing.T) {
	s, _ := securedServer(t)
	receiver := &publicationReceiver{}
	if err := s.ConfigurePublicationNotifications(notificationToken, []string{"ghcr.io/acme/app"}, receiver); err != nil {
		t.Fatal(err)
	}
	if w := publicationRequest(t, s, publicationBody(t), notificationToken); w.Code != 200 {
		t.Fatalf("legacy ingest=%d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/api/projects", "/api/auth/users", "/api/auth/keys", "/api/containers/id/update", "/api/unknown"} {
		for _, method := range []string{"GET", "POST"} {
			if w := authRequest(s, method, path, nil, nil, notificationToken); w.Code != 401 {
				t.Errorf("legacy %s %s=%d", method, path, w.Code)
			}
		}
	}
}
func adminSession(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	w := authRequest(s, "POST", "/api/auth/setup", map[string]string{"username": "admin", "password": "a sufficiently long password", "setupToken": "test-bootstrap-token-that-is-at-least-32-bytes"}, nil, "")
	if w.Code != 201 {
		t.Fatalf("setup=%d %s", w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}
func TestAdminKeyLifecycleAndLogout(t *testing.T) {
	s, _ := securedServer(t)
	cookie := adminSession(t, s)
	w := authRequest(s, "POST", "/api/auth/keys", map[string]any{"name": "reader", "scopes": []string{"services:read"}, "repositories": []string{}}, cookie, "")
	if w.Code != 201 {
		t.Fatalf("create=%d %s", w.Code, w.Body.String())
	}
	var created struct {
		Key struct {
			ID string `json:"id"`
		}
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Token == "" || created.Key.ID == "" {
		t.Fatalf("missing key=%s", w.Body.String())
	}
	w = authRequest(s, "GET", "/api/auth/keys", nil, cookie, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), created.Token) {
		t.Fatalf("list leaked secret=%d %s", w.Code, w.Body.String())
	}
	if w = authRequest(s, "GET", "/api/version", nil, nil, created.Token); w.Code != 200 {
		t.Fatalf("read=%d", w.Code)
	}
	if w = authRequest(s, "DELETE", "/api/auth/keys/"+created.Key.ID, nil, cookie, ""); w.Code != 204 {
		t.Fatalf("revoke=%d %s", w.Code, w.Body.String())
	}
	if w = authRequest(s, "GET", "/api/version", nil, nil, created.Token); w.Code != 401 {
		t.Fatalf("revoked=%d", w.Code)
	}
}
func TestAdminUserLifecycleAndPasswordInvalidatesSession(t *testing.T) {
	s, _ := securedServer(t)
	cookie := adminSession(t, s)
	w := authRequest(s, "POST", "/api/auth/users", map[string]string{"username": "second", "password": "another sufficiently long password"}, cookie, "")
	if w.Code != 201 {
		t.Fatalf("create user=%d %s", w.Code, w.Body.String())
	}
	var created struct {
		User struct {
			ID string `json:"id"`
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if w = authRequest(s, "DELETE", "/api/auth/users/"+created.User.ID, nil, cookie, ""); w.Code != 204 {
		t.Fatalf("delete=%d", w.Code)
	}
	if w = authRequest(s, "PUT", "/api/auth/password", map[string]string{"password": "a replacement long password"}, cookie, ""); w.Code != 204 {
		t.Fatalf("password=%d %s", w.Code, w.Body.String())
	}
	if w = authRequest(s, "GET", "/api/version", nil, cookie, ""); w.Code != 401 {
		t.Fatalf("old session=%d", w.Code)
	}
	if w = authRequest(s, "POST", "/api/auth/login", map[string]string{"username": "admin", "password": "a replacement long password"}, nil, ""); w.Code != 200 {
		t.Fatalf("login=%d %s", w.Code, w.Body.String())
	}
}
func TestAPIKeysNeverFallBackToAdminCookieOrAllowBrowserUse(t *testing.T) {
	s, store := securedServer(t)
	cookie := adminSession(t, s)
	_, token, err := store.CreateKey("reader", []string{"services:read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := authRequest(s, "GET", "/api/auth/keys", nil, cookie, token); w.Code != 403 {
		t.Fatalf("escalation=%d", w.Code)
	}
	if w := authRequest(s, "GET", "/api/version", nil, cookie, "invalid"); w.Code != 401 {
		t.Fatalf("fallback=%d", w.Code)
	}
	r := httptest.NewRequest("GET", "http://localhost/api/version", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Origin", "http://localhost")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("browser key=%d", w.Code)
	}
}
func TestAuthenticationRejectsInvalidJSONAndRateLimitsLogin(t *testing.T) {
	s, _ := securedServer(t)
	for i := 0; i < 11; i++ {
		r := httptest.NewRequest("POST", "http://localhost/api/auth/login", bytes.NewBufferString(`{"username":"nobody","password":"anything","unknown":true}`))
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want := 400
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d=%d want%d", i, w.Code, want)
		}
	}
}
func TestSecureSessionCookieUsesConfiguredHTTPSOrigin(t *testing.T) {
	s, store := securedServer(t)
	s.publicOrigin = &originAuthority{scheme: "https", host: "services.example", port: "443"}
	s.allowedHosts["services.example"] = struct{}{}
	if _, err := store.Bootstrap("admin", "a sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://services.example/api/auth/login", strings.NewReader(`{"username":"admin","password":"a sufficiently long password"}`))
	r.Header.Set("Origin", "https://services.example")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("login=%d %s", w.Code, w.Body.String())
	}
	c := w.Result().Cookies()
	if len(c) != 1 || !c[0].Secure || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatal(fmt.Sprintf("cookie=%#v", c))
	}
}

func TestSecureSessionCookieUsesDirectTLS(t *testing.T) {
	s, store := securedServer(t)
	if _, err := store.Bootstrap("admin", "a sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "https://localhost/api/auth/login", strings.NewReader(`{"username":"admin","password":"a sufficiently long password"}`))
	r.Header.Set("Origin", "https://localhost")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("login=%d %s", w.Code, w.Body.String())
	}
	if cookies := w.Result().Cookies(); len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("TLS session not Secure: %#v", cookies)
	}
}
