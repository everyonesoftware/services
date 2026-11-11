package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"services/internal/auth"
	"services/internal/githubapp"
	"services/internal/publishers"
)

type publisherProviderFake struct {
	repositories []githubapp.Repository
	configured   int
	dispatched   int
	enabled      int
	challenge    string
	configureErr error
	dispatchErr  error
	enableErr    error
}

func (f *publisherProviderFake) ExchangeManifest(context.Context, string) (githubapp.App, error) {
	return githubapp.App{ID: 1, Slug: "services-test", Name: "Services", PEM: "private-app-key"}, nil
}
func (f *publisherProviderFake) CheckPermissions(context.Context, githubapp.App) (githubapp.PermissionStatus, error) {
	return githubapp.PermissionStatus{VariablesWrite: true, SettingsURL: "https://github.com/settings/apps/services-test/permissions"}, nil
}
func (f *publisherProviderFake) VerifyRepositoryPermissions(context.Context, githubapp.App, githubapp.Repository) error {
	return nil
}
func (f *publisherProviderFake) Repositories(context.Context, githubapp.App) ([]githubapp.Repository, error) {
	return f.repositories, nil
}
func (f *publisherProviderFake) Inspect(context.Context, githubapp.App, githubapp.Repository) (githubapp.Inspection, error) {
	return githubapp.Inspection{Supported: true, Problems: []string{}}, nil
}
func (f *publisherProviderFake) Configure(_ context.Context, _ githubapp.App, _ githubapp.Repository, _ string, _ map[string]string) error {
	f.configured++
	return f.configureErr
}
func (f *publisherProviderFake) SetEnabled(_ context.Context, _ githubapp.App, _ githubapp.Repository, enabled bool) error {
	if enabled {
		f.enabled++
	}
	return f.enableErr
}
func (f *publisherProviderFake) DispatchTest(_ context.Context, _ githubapp.App, _ githubapp.Repository, _ string, challenge string) error {
	f.dispatched++
	f.challenge = challenge
	return f.dispatchErr
}
func publisherServer(t *testing.T) (*Server, *auth.Store, *publishers.Store, *publisherProviderFake, *http.Cookie) {
	t.Helper()
	s, authStore := securedServer(t)
	cookie := adminSession(t, s)
	s.publicOrigin = &originAuthority{scheme: "https", host: "localhost", port: "443"}
	if err := s.ConfigurePublicationReceiver(&publicationReceiver{}); err != nil {
		t.Fatal(err)
	}
	store, err := publishers.Open(filepath.Join(t.TempDir(), "publishers"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	repo := githubapp.Repository{ID: 12, FullName: "acme/app", DefaultBranch: "main", InstallationID: 7}
	provider := &publisherProviderFake{repositories: []githubapp.Repository{repo}}
	if err = s.ConfigurePublishers(store, provider); err != nil {
		t.Fatal(err)
	}
	if err = store.Save(publishers.Snapshot{App: publishers.App{ID: 1, Slug: "services-test", Name: "Services", PEM: "private-app-key"}, Tailnet: publishers.Tailnet{ClientID: "client-id", Audience: "audience", Tags: "tag:publisher"}, Connections: []publishers.Connection{}}); err != nil {
		t.Fatal(err)
	}
	return s, authStore, store, provider, cookie
}
func publisherSetupBody() map[string]any {
	repo := githubapp.Repository{ID: 12, FullName: "acme/app", DefaultBranch: "main", InstallationID: 7}
	variables := map[string]string{"SERVICES_PUBLISH_URL": "https://localhost/api/images/published", "SERVICES_PUBLISH_NOTIFICATIONS": "false", "TAILSCALE_CLIENT_ID": "client-id", "TAILSCALE_AUDIENCE": "audience", "TAILSCALE_TAGS": "tag:publisher"}
	return map[string]any{"repository": repo, "imageRepository": "ghcr.io/acme/app", "approval": publisherApproval(repo, "ghcr.io/acme/app", variables, nil)}
}
func publisherRequest(s *Server, method, path string, body any, cookie *http.Cookie, token string) *httptest.ResponseRecorder {
	var b bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&b).Encode(body)
	}
	r := httptest.NewRequest(method, "https://localhost"+path, &b)
	r.Header.Set("Origin", "https://localhost")
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
func TestPublisherSetupRequiresAdminAndNeverReturnsSecrets(t *testing.T) {
	authRequest := publisherRequest
	s, keys, _, provider, cookie := publisherServer(t)
	_, token, err := keys.CreateKey("reader", []string{"services:read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/publishers", "/api/publishers/repositories"} {
		if w := authRequest(s, "GET", path, nil, nil, token); w.Code != 403 {
			t.Fatalf("read key allowed %s: %d", path, w.Code)
		}
	}
	if w := authRequest(s, "POST", "/api/publishers", publisherSetupBody(), nil, ""); w.Code != 401 {
		t.Fatalf("anonymous setup=%d", w.Code)
	}
	w := authRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, "")
	if w.Code != 201 {
		t.Fatalf("setup=%d %s", w.Code, w.Body.String())
	}
	if provider.configured != 1 || provider.dispatched != 1 || provider.enabled != 0 {
		t.Fatalf("setup/config/test/enable=%d/%d/%d", provider.configured, provider.dispatched, provider.enabled)
	}
	for _, path := range []string{"/api/publishers", "/api/publishers/repositories"} {
		w = authRequest(s, "GET", path, nil, cookie, "")
		if w.Code != 200 {
			t.Fatalf("list=%d", w.Code)
		}
		for _, secret := range []string{"private-app-key", provider.challenge, "svc_"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("secret leaked: %s", path)
			}
		}
	}
}
func TestPublisherCallbackIsBoundToKeyChallengeRepositoryAndRef(t *testing.T) {
	authRequest := publisherRequest
	s, keys, store, provider, cookie := publisherServer(t)
	w := authRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, "")
	if w.Code != 201 {
		t.Fatalf("setup=%d %s", w.Code, w.Body.String())
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	connection := state.Connections[0]
	other, otherToken, err := keys.CreateKey("other", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = other
	body := map[string]string{"connectionId": connection.ID, "challenge": provider.challenge, "sourceRepository": "acme/app", "ref": "refs/heads/main", "runId": "123"}
	if r := authRequest(s, "POST", "/api/publishers/callback", body, nil, otherToken); r.Code != 403 {
		t.Fatalf("wrong key=%d %s", r.Code, r.Body.String())
	}
	body["ref"] = "refs/heads/other"
	if r := authRequest(s, "POST", "/api/publishers/callback", body, nil, connection.Secret); r.Code != 403 {
		t.Fatalf("wrong ref=%d", r.Code)
	}
	body["ref"] = "refs/heads/main"
	body["challenge"] = "wrong"
	if r := authRequest(s, "POST", "/api/publishers/callback", body, nil, connection.Secret); r.Code != 403 {
		t.Fatalf("wrong challenge=%d", r.Code)
	}
	body["challenge"] = provider.challenge
	w = authRequest(s, "POST", "/api/publishers/callback", body, nil, connection.Secret)
	if w.Code != 200 {
		t.Fatalf("callback=%d %s", w.Code, w.Body.String())
	}
	if provider.enabled != 1 {
		t.Fatal("notifications not enabled after callback")
	}
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Connections[0].State != "verified" || state.Connections[0].VerifiedAt == "" {
		t.Fatal("callback not persisted")
	}
	w = authRequest(s, "POST", "/api/publishers/callback", body, nil, connection.Secret)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "duplicate") || provider.enabled != 1 {
		t.Fatalf("duplicate=%d calls=%d", w.Code, provider.enabled)
	}
	if w = authRequest(s, "POST", "/api/publishers/callback", body, cookie, ""); w.Code != 403 {
		t.Fatalf("cookie callback=%d", w.Code)
	}
}
func TestPublisherIgnoresForgedRepositoryAndAvoidsDuplicateKeys(t *testing.T) {
	authRequest := publisherRequest
	s, keys, _, provider, cookie := publisherServer(t)
	body := publisherSetupBody()
	repo := body["repository"].(githubapp.Repository)
	repo.InstallationID = 999
	body["repository"] = repo
	if w := authRequest(s, "POST", "/api/publishers", body, cookie, ""); w.Code != 403 {
		t.Fatalf("forged install=%d %s", w.Code, w.Body.String())
	}
	if provider.configured != 0 {
		t.Fatal("forged repo reached mutation")
	}
	body = publisherSetupBody()
	if w := authRequest(s, "POST", "/api/publishers", body, cookie, ""); w.Code != 201 {
		t.Fatalf("setup=%d", w.Code)
	}
	if w := authRequest(s, "POST", "/api/publishers", body, cookie, ""); w.Code != 409 {
		t.Fatalf("duplicate setup=%d", w.Code)
	}
	list, err := keys.ListKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("duplicate keys=%d", len(list))
	}
}
func TestPublisherStartUsesBoundStateAndDoesNotExposePEM(t *testing.T) {
	authRequest := publisherRequest
	s, _, store, _, cookie := publisherServer(t)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	state.App = publishers.App{}
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	w := authRequest(s, "POST", "/api/publishers/github/start", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("start=%d %s", w.Code, w.Body.String())
	}
	var start struct{ Action, Manifest string }
	if err = json.Unmarshal(w.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(start.Action, "https://github.com/settings/apps/new?state=") || !strings.Contains(start.Manifest, "/github-app-callback") {
		t.Fatal("manifest missing trusted redirect")
	}
	w = authRequest(s, "POST", "/api/publishers/github/complete", map[string]string{"code": "code", "state": "wrong"}, cookie, "")
	if w.Code != 403 {
		t.Fatalf("invalid state=%d", w.Code)
	}
}
func TestPublisherCallbackRejectsExpiredChallenge(t *testing.T) {
	authRequest := publisherRequest
	s, _, store, provider, cookie := publisherServer(t)
	w := authRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, "")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	state, _ := store.Load()
	state.Connections[0].ChallengeExpires = time.Now().Add(-time.Second).Unix()
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	c := state.Connections[0]
	body := map[string]string{"connectionId": c.ID, "challenge": provider.challenge, "sourceRepository": "acme/app", "ref": "refs/heads/main", "runId": "123"}
	if w = authRequest(s, "POST", "/api/publishers/callback", body, nil, c.Secret); w.Code != 403 || provider.enabled != 0 {
		t.Fatalf("expired=%d enabled=%d", w.Code, provider.enabled)
	}
}
func TestPublisherMutationsRejectCrossOrigin(t *testing.T) {
	s, _, _, provider, cookie := publisherServer(t)
	r := httptest.NewRequest("POST", "http://localhost/api/publishers", strings.NewReader(`{}`))
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://attacker.invalid")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 || provider.configured != 0 {
		t.Fatalf("cross origin=%d", w.Code)
	}
}
