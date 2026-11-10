package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"services/internal/githubapp"
	"services/internal/publishers"
)

type lifecycleProvider struct {
	*publisherProviderFake
	recovered, rotated, disabled int
	replacement                  string
	recoveryErr, replacementErr  error
}

func (p *lifecycleProvider) RecoverConfiguration(context.Context, githubapp.App, githubapp.Repository, string, map[string]string) error {
	p.recovered++
	return p.recoveryErr
}
func (p *lifecycleProvider) ReplaceSecret(_ context.Context, _ githubapp.App, _ githubapp.Repository, secret string) error {
	p.rotated++
	p.replacement = secret
	return p.replacementErr
}
func (p *lifecycleProvider) SetEnabled(ctx context.Context, a githubapp.App, r githubapp.Repository, enabled bool) error {
	if !enabled {
		p.disabled++
	}
	return p.publisherProviderFake.SetEnabled(ctx, a, r, enabled)
}
func connectionForTest(t *testing.T, s *Server, store *publishers.Store, cookie *http.Cookie) publishers.Connection {
	t.Helper()
	w := publisherRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, "")
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return state.Connections[0]
}
func callbackBody(c publishers.Connection, challenge string) map[string]string {
	return map[string]string{"connectionId": c.ID, "challenge": challenge, "sourceRepository": c.Repository.FullName, "ref": "refs/heads/" + c.Repository.DefaultBranch, "runId": "123"}
}
func TestPublisherDisconnectRevokesCredentialAndInvalidatesPendingProof(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &lifecycleProvider{publisherProviderFake: base}
	s.publishers.provider = p
	c := connectionForTest(t, s, store, cookie)
	proof := callbackBody(c, base.challenge)
	w := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/disconnect", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("disconnect: %d %s", w.Code, w.Body.String())
	}
	if _, err := keys.AuthenticateKey(c.Secret); err == nil {
		t.Fatal("disconnected credential remains active")
	}
	state, _ := store.Load()
	if state.Connections[0].State != "disconnected" || state.Connections[0].Secret != "" {
		t.Fatal("disconnect not persisted")
	}
	if p.disabled != 1 {
		t.Fatal("notification configuration not disabled")
	}
	if w = publisherRequest(s, "POST", "/api/publishers/callback", proof, nil, c.Secret); w.Code != 401 {
		t.Fatalf("late proof: %d", w.Code)
	}
	if w = publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/test", map[string]any{}, cookie, ""); w.Code != 409 {
		t.Fatalf("disconnected test: %d", w.Code)
	}
}
func TestPublisherDisconnectStillRevokesLocalKeyWhenGitHubUnavailable(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &lifecycleProvider{publisherProviderFake: base}
	s.publishers.provider = p
	c := connectionForTest(t, s, store, cookie)
	base.enableErr = errors.New("offline")
	w := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/disconnect", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("disconnect: %d %s", w.Code, w.Body.String())
	}
	if _, err := keys.AuthenticateKey(c.Secret); err == nil {
		t.Fatal("offline disconnect kept key")
	}
	if !strings.Contains(w.Body.String(), "GitHub") {
		t.Fatal("remote cleanup failure not visible")
	}
}
func TestPublisherRotationKeepsOldKeyUntilNewAuthenticatedCallback(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &lifecycleProvider{publisherProviderFake: base}
	s.publishers.provider = p
	c := connectionForTest(t, s, store, cookie)
	if w := publisherRequest(s, "POST", "/api/publishers/callback", callbackBody(c, base.challenge), nil, c.Secret); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/rotate", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("rotation: %d %s", w.Code, w.Body.String())
	}
	if _, err := keys.AuthenticateKey(c.Secret); err != nil {
		t.Fatal("old key revoked before verification")
	}
	state, _ := store.Load()
	next := state.Connections[0]
	if next.KeyID == c.KeyID || next.Secret == c.Secret || next.State != "awaiting_callback" {
		t.Fatal("rotation not pending")
	}
	if p.replacement != next.Secret || strings.Contains(w.Body.String(), next.Secret) {
		t.Fatal("secret transfer or disclosure")
	}
	if w = publisherRequest(s, "POST", "/api/publishers/callback", callbackBody(next, base.challenge), nil, c.Secret); w.Code != 403 {
		t.Fatalf("old key verifies replacement: %d", w.Code)
	}
	if w = publisherRequest(s, "POST", "/api/publishers/callback", callbackBody(next, base.challenge), nil, next.Secret); w.Code != 200 {
		t.Fatalf("new proof: %d %s", w.Code, w.Body.String())
	}
	if _, err := keys.AuthenticateKey(c.Secret); err == nil {
		t.Fatal("old key not revoked after verified replacement")
	}
	list, _ := keys.ListKeys()
	if len(list) != 1 {
		t.Fatal("rotation leaked extra active keys")
	}
}
func TestPublisherRecoveryReusesSavedKeyAfterUncertainProvisioning(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &lifecycleProvider{publisherProviderFake: base}
	s.publishers.provider = p
	base.configureErr = errors.New("transport failed after write")
	w := publisherRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, "")
	if w.Code != 409 {
		t.Fatalf("uncertain setup: %d", w.Code)
	}
	state, _ := store.Load()
	c := state.Connections[0]
	if c.Secret == "" {
		t.Fatal("recoverable key was discarded")
	}
	w = publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/recover", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("recover: %d %s", w.Code, w.Body.String())
	}
	list, _ := keys.ListKeys()
	if len(list) != 1 || list[0].ID != c.KeyID || p.recovered != 1 {
		t.Fatal("recovery minted duplicate credential")
	}
	state, _ = store.Load()
	if state.Connections[0].State != "awaiting_callback" {
		t.Fatal("recovery bypassed authenticated test")
	}
}
func TestPublisherShowsOnlyRealAcceptedPublicationForManagedConnection(t *testing.T) {
	s, _, store, base, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	if w := publisherRequest(s, "POST", "/api/publishers/callback", callbackBody(c, base.challenge), nil, c.Secret); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	if strings.Contains(w.Body.String(), "lastPublication") {
		t.Fatal("test callback invented publication")
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(publicationBody(t)), &body); err != nil {
		t.Fatal(err)
	}
	if w = publisherRequest(s, "POST", "/api/images/published", body, nil, c.Secret); w.Code != 200 {
		t.Fatalf("publication: %d %s", w.Code, w.Body.String())
	}
	w = publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	if !strings.Contains(w.Body.String(), "lastPublication") || !strings.Contains(w.Body.String(), `"version":"42"`) {
		t.Fatalf("missing receipt: %s", w.Body.String())
	}
	body["sourceRepository"] = "other/app"
	body["runUrl"] = "https://github.com/other/app/actions/runs/123"
	body["version"] = "forged"
	if w = publisherRequest(s, "POST", "/api/images/published", body, nil, c.Secret); w.Code != 403 {
		t.Fatalf("wrong source accepted: %d", w.Code)
	}
}

func TestPublisherStatusReadDoesNotChangeCredentialLastUsed(t *testing.T) {
	s, keys, store, _, cookie := publisherServer(t)
	connectionForTest(t, s, store, cookie)
	before, _ := keys.ListKeys()
	if before[0].LastUsedAt != "" {
		t.Fatal("unexpected fixture key use")
	}
	if w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	after, _ := keys.ListKeys()
	if after[0].LastUsedAt != "" {
		t.Fatal("status polling masquerades as publisher activity")
	}
}

func TestPublisherLifecycleActionsRequireAdministratorSession(t *testing.T) {
	s, keys, store, _, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	for _, action := range []string{"recover", "rotate", "disconnect"} {
		path := "/api/publishers/" + c.ID + "/" + action
		if w := publisherRequest(s, "POST", path, map[string]any{}, nil, ""); w.Code != 401 {
			t.Fatalf("anonymous %s: %d", action, w.Code)
		}
		if w := publisherRequest(s, "POST", path, map[string]any{}, nil, c.Secret); w.Code != 403 {
			t.Fatalf("key %s: %d", action, w.Code)
		}
	}
	if _, err := keys.AuthenticateKey(c.Secret); err != nil {
		t.Fatal("rejected lifecycle request mutated credential")
	}
}

func TestPublisherRotationRetryReusesReplacementAfterUncertainSecretWrite(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &lifecycleProvider{publisherProviderFake: base, replacementErr: errors.New("uncertain")}
	s.publishers.provider = p
	c := connectionForTest(t, s, store, cookie)
	path := "/api/publishers/" + c.ID + "/rotate"
	if w := publisherRequest(s, "POST", path, map[string]any{}, cookie, ""); w.Code != 502 {
		t.Fatalf("uncertain: %d", w.Code)
	}
	first := p.replacement
	p.replacementErr = nil
	if w := publisherRequest(s, "POST", path, map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	list, _ := keys.ListKeys()
	if len(list) != 2 || p.replacement != first {
		t.Fatal("retry leaked a third credential")
	}
}

func TestPublisherInterruptedProvisioningBecomesRecoverableAfterRestart(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	connectionForTest(t, s, store, cookie)
	state, _ := store.Load()
	state.Connections[0].State = "configured"
	state.Connections[0].Configured = false
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Recover setup") {
		t.Fatalf("interrupted setup not recoverable: %d %s", w.Code, w.Body.String())
	}
}
