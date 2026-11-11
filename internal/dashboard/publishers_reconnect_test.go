package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"services/internal/githubapp"
	"services/internal/publishers"
	"strings"
	"testing"
)

type reconnectProvider struct {
	*lifecycleProvider
	reconnectErr    error
	reconnectCalls  int
	inspectErr      error
	unsupported     bool
	permissionErr   error
	beforeInspect   func()
	beforeReconnect func(string, map[string]string)
}

func (p *reconnectProvider) Inspect(context.Context, githubapp.App, githubapp.Repository) (githubapp.Inspection, error) {
	if p.beforeInspect != nil {
		p.beforeInspect()
	}
	return githubapp.Inspection{Supported: !p.unsupported}, p.inspectErr
}
func (p *reconnectProvider) VerifyRepositoryPermissions(context.Context, githubapp.App, githubapp.Repository) error {
	return p.permissionErr
}
func (p *reconnectProvider) ReconnectConfiguration(_ context.Context, _ githubapp.App, _ githubapp.Repository, secret string, variables map[string]string) error {
	p.reconnectCalls++
	if p.beforeReconnect != nil {
		p.beforeReconnect(secret, variables)
	}
	return p.reconnectErr
}

func TestPublisherReconnectFreshKeyRequiresCallback(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &reconnectProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}}
	s.publishers.provider = p
	old := connectionForTest(t, s, store, cookie)
	path := "/api/publishers/" + old.ID + "/reconnect"
	if w := publisherRequest(s, "POST", path, map[string]any{}, nil, ""); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	if w := publisherRequest(s, "POST", path, map[string]any{}, nil, old.Secret); w.Code != 403 {
		t.Fatalf("key: %d", w.Code)
	}
	if w := publisherRequest(s, "POST", path, map[string]any{}, cookie, ""); w.Code != 409 {
		t.Fatalf("active: %d %s", w.Code, w.Body.String())
	}
	oldProof := callbackBody(old, base.challenge)
	if w := publisherRequest(s, "POST", "/api/publishers/"+old.ID+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p.beforeReconnect = func(secret string, variables map[string]string) {
		saved, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		c := saved.Connections[0]
		if secret == old.Secret || secret == "" || c.Secret != secret || c.KeyID == old.KeyID || c.Configured || c.ChallengeHash != "" || !c.RemoteDisablePending {
			t.Fatal("fresh credential was not saved before remote mutation")
		}
		if variables["SERVICES_PUBLISH_NOTIFICATIONS"] != "false" {
			t.Fatal("enabled before callback")
		}
	}
	w := publisherRequest(s, "POST", path, map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("reconnect: %d %s", w.Code, w.Body.String())
	}
	state, _ := store.Load()
	c := state.Connections[0]
	if c.ID != old.ID || c.Repository != old.Repository || c.ImageRepository != old.ImageRepository || c.CreatedAt != old.CreatedAt || c.State != "awaiting_callback" || !c.Configured || c.RemoteDisablePending {
		t.Fatalf("reconnected identity/status: %+v", connectionView(c))
	}
	if strings.Contains(w.Body.String(), c.Secret) || p.reconnectCalls != 1 || base.configured != 1 || base.enabled != 0 {
		t.Fatal("unsafe reconnect or response")
	}
	if _, err := keys.AuthenticateKey(old.Secret); err == nil {
		t.Fatal("revoked credential restored")
	}
	if w = publisherRequest(s, "POST", "/api/publishers/callback", oldProof, nil, c.Secret); w.Code != 403 {
		t.Fatalf("stale challenge: %d", w.Code)
	}
	if w = publisherRequest(s, "POST", "/api/publishers/callback", callbackBody(c, base.challenge), nil, c.Secret); w.Code != 200 {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	if base.enabled != 1 {
		t.Fatal("callback did not enable")
	}
	list, _ := keys.ListKeys()
	if len(list) != 1 || list[0].ID != c.KeyID {
		t.Fatal("unexpected active credentials")
	}
}

func TestPublisherReconnectRetryReusesSavedCredential(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &reconnectProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}, reconnectErr: errors.New("uncertain write")}
	s.publishers.provider = p
	old := connectionForTest(t, s, store, cookie)
	path := "/api/publishers/" + old.ID
	if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "POST", path+"/reconnect", map[string]any{}, cookie, ""); w.Code != 502 {
		t.Fatalf("uncertain: %d %s", w.Code, w.Body.String())
	}
	state, _ := store.Load()
	pending := state.Connections[0]
	if pending.State != "disconnected" || pending.Secret == "" || pending.Configured || !pending.RemoteDisablePending {
		t.Fatal("uncertain reconnect state lost")
	}
	if w := publisherRequest(s, "POST", "/api/publishers/callback", callbackBody(pending, base.challenge), nil, pending.Secret); w.Code != 403 {
		t.Fatalf("uncertain reconnect callback: %d", w.Code)
	}
	if w := publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 409 {
		t.Fatalf("pending remove: %d", w.Code)
	}
	p.reconnectErr = nil
	if w := publisherRequest(s, "POST", path+"/reconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	state, _ = store.Load()
	next := state.Connections[0]
	keysList, _ := keys.ListKeys()
	if next.Secret != pending.Secret || next.KeyID != pending.KeyID || len(keysList) != 1 {
		t.Fatal("retry minted duplicate credential instead of reusing durable replacement")
	}
}

func TestPublisherReconnectCannotTakeOverActiveSuccessor(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &reconnectProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}}
	s.publishers.provider = p
	old := connectionForTest(t, s, store, cookie)
	if w := publisherRequest(s, "POST", "/api/publishers/"+old.ID+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, ""); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	before, _ := store.Load()
	w := publisherRequest(s, "POST", "/api/publishers/"+old.ID+"/reconnect", map[string]any{}, cookie, "")
	if w.Code != 409 {
		t.Fatalf("successor takeover: %d %s", w.Code, w.Body.String())
	}
	after, _ := store.Load()
	list, _ := keys.ListKeys()
	if !reflect.DeepEqual(before, after) || p.reconnectCalls != 0 || len(list) != 1 {
		t.Fatal("successor changed")
	}
}

func TestPublisherReconnectRevokedPendingKeyIsReplaced(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &reconnectProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}, reconnectErr: errors.New("uncertain")}
	s.publishers.provider = p
	old := connectionForTest(t, s, store, cookie)
	path := "/api/publishers/" + old.ID
	if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "POST", path+"/reconnect", map[string]any{}, cookie, ""); w.Code != 502 {
		t.Fatal(w.Body.String())
	}
	state, _ := store.Load()
	pending := state.Connections[0]
	if err := keys.RevokeKey(pending.KeyID); err != nil {
		t.Fatal(err)
	}
	p.reconnectErr = nil
	if w := publisherRequest(s, "POST", path+"/reconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	state, _ = store.Load()
	next := state.Connections[0]
	if next.KeyID == pending.KeyID || next.Secret == pending.Secret {
		t.Fatal("revoked pending key reused")
	}
	if _, err := keys.AuthenticateKey(next.Secret); err != nil {
		t.Fatal("replacement not active", err)
	}
	if _, err := keys.AuthenticateKey(pending.Secret); err == nil {
		t.Fatal("revoked key restored")
	}
}

func TestPublisherReconnectDispatchSaveFailureRemainsRecoverable(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &reconnectProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}}
	s.publishers.provider = p
	old := connectionForTest(t, s, store, cookie)
	path := "/api/publishers/" + old.ID
	if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	state, _ := store.Load()
	directory := filepath.Join(t.TempDir(), "reconnect")
	store, err := publishers.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	s.publishers.store = store
	db, err := sql.Open("sqlite", filepath.Join(directory, "publishers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p.beforeReconnect = func(string, map[string]string) {
		_, err = db.Exec(`CREATE TABLE checkpoints(n INTEGER); INSERT INTO checkpoints VALUES(0);
   CREATE TRIGGER fail_dispatch BEFORE UPDATE ON publisher_state WHEN (SELECT n FROM checkpoints)=1 BEGIN SELECT RAISE(FAIL,'dispatch save failed'); END;
   CREATE TRIGGER count_completion AFTER UPDATE ON publisher_state BEGIN UPDATE checkpoints SET n=n+1; END;`)
		if err != nil {
			t.Fatal(err)
		}
	}
	w := publisherRequest(s, "POST", path+"/reconnect", map[string]any{}, cookie, "")
	if w.Code != 502 {
		t.Fatalf("dispatch persistence failure: %d %s", w.Code, w.Body.String())
	}
	if _, err = db.Exec(`DROP TRIGGER fail_dispatch; DROP TRIGGER count_completion`); err != nil {
		t.Fatal(err)
	}
	if w = publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 409 {
		t.Fatalf("removal would orphan reconnect credential: %d %s", w.Code, w.Body.String())
	}
	if w = publisherRequest(s, "POST", path+"/recover", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatalf("recover dispatch: %d %s", w.Code, w.Body.String())
	}
	list, _ := keys.ListKeys()
	if len(list) != 1 {
		t.Fatal("failure leaked credential")
	}
}

func TestPublisherReconnectPreflightLeavesDisconnectedCredentialUntouched(t *testing.T) {
	for _, failure := range []string{"access", "permissions", "workflow", "inspect", "tailnet"} {
		t.Run(failure, func(t *testing.T) {
			s, keys, store, base, cookie := publisherServer(t)
			p := &reconnectProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}}
			s.publishers.provider = p
			old := connectionForTest(t, s, store, cookie)
			path := "/api/publishers/" + old.ID
			if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			state, _ := store.Load()
			want := 403
			switch failure {
			case "access":
				base.repositories = nil
			case "permissions":
				p.permissionErr = errors.New("permission removed")
			case "workflow":
				p.unsupported = true
				want = 409
			case "inspect":
				p.inspectErr = errors.New("offline")
				want = 502
			case "tailnet":
				state.Tailnets["12"] = publishers.Tailnet{}
				if err := store.Save(state); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := store.Load()
			w := publisherRequest(s, "POST", path+"/reconnect", map[string]any{}, cookie, "")
			if w.Code != want {
				t.Fatalf("preflight: %d %s", w.Code, w.Body.String())
			}
			after, _ := store.Load()
			list, _ := keys.ListKeys()
			if !reflect.DeepEqual(before.Connections, after.Connections) || p.reconnectCalls != 0 || len(list) != 0 {
				t.Fatal("preflight failure mutated connection or minted credential")
			}
		})
	}
}

func TestPublisherReconnectStorageFailureRetainsSafeRetry(t *testing.T) {
	for _, failure := range []string{"credential", "completion", "remove"} {
		t.Run(failure, func(t *testing.T) {
			s, keys, store, base, cookie := publisherServer(t)
			p := &reconnectProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}}
			s.publishers.provider = p
			old := connectionForTest(t, s, store, cookie)
			path := "/api/publishers/" + old.ID
			if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			before, _ := store.Load()
			directory := filepath.Join(t.TempDir(), "failure")
			store, err := publishers.Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.Save(before); err != nil {
				t.Fatal(err)
			}
			s.publishers.store = store
			db, err := sql.Open("sqlite", filepath.Join(directory, "publishers.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			failSave := func() {
				if _, err := db.Exec(`CREATE TRIGGER fail_save BEFORE UPDATE ON publisher_state BEGIN SELECT RAISE(FAIL,'save unavailable'); END`); err != nil {
					t.Fatal(err)
				}
			}
			method, target, body := "POST", path+"/reconnect", any(map[string]any{})
			switch failure {
			case "credential":
				p.beforeInspect = failSave
			case "completion":
				p.beforeReconnect = func(string, map[string]string) { failSave() }
			case "remove":
				failSave()
				method = "DELETE"
				target = path
				body = nil
			}
			w := publisherRequest(s, method, target, body, cookie, "")
			if w.Code != 503 {
				t.Fatalf("save failure: %d %s", w.Code, w.Body.String())
			}
			saved, _ := store.Load()
			list, _ := keys.ListKeys()
			if failure == "completion" {
				if saved.Connections[0].Secret == "" || !saved.Connections[0].RemoteDisablePending || len(list) != 1 {
					t.Fatal("completion failure discarded durable retry")
				}
			} else if !reflect.DeepEqual(before.Connections, saved.Connections) || len(list) != 0 || p.reconnectCalls != 0 {
				t.Fatal("failed persistence mutated state or remote configuration")
			}
			if _, err = db.Exec(`DROP TRIGGER fail_save`); err != nil {
				t.Fatal(err)
			}
			p.beforeInspect = nil
			p.beforeReconnect = nil
			if w = publisherRequest(s, method, target, body, cookie, ""); w.Code != 200 {
				t.Fatalf("retry: %d %s", w.Code, w.Body.String())
			}
			if failure == "completion" {
				after, _ := store.Load()
				if after.Connections[0].KeyID != saved.Connections[0].KeyID {
					t.Fatal("retry lost saved credential")
				}
			}
		})
	}
}

func TestPublisherRemoveBlocksPendingCleanup(t *testing.T) {
	s, _, store, base, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	base.enableErr = errors.New("offline")
	path := "/api/publishers/" + c.ID
	if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	before, _ := store.Load()
	w := publisherRequest(s, "DELETE", path, nil, cookie, "")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "disconnect") {
		t.Fatalf("pending cleanup removal: %d %s", w.Code, w.Body.String())
	}
	after, _ := store.Load()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("blocked removal changed saved state")
	}
	base.enableErr = nil
	if w = publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestPublisherRemoveDisconnectedRecordOnly(t *testing.T) {
	s, _, store, base, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	path := "/api/publishers/" + c.ID
	for _, request := range []struct{ method, path string }{{"DELETE", path}} {
		if w := publisherRequest(s, request.method, request.path, map[string]any{}, nil, ""); w.Code != 401 {
			t.Fatalf("anonymous: %d", w.Code)
		}
		if w := publisherRequest(s, request.method, request.path, map[string]any{}, nil, c.Secret); w.Code != 403 {
			t.Fatalf("key: %d", w.Code)
		}
		if w := publisherRequest(s, request.method, request.path, map[string]any{}, cookie, ""); w.Code != 409 {
			t.Fatalf("active %s: %d %s", request.method, w.Code, w.Body.String())
		}
	}
	if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	before, _ := store.Load()
	calls := []int{base.configured, base.dispatched, base.enabled}
	w := publisherRequest(s, "DELETE", path, nil, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"removed":true`) {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	after, _ := store.Load()
	if len(after.Connections) != 0 || !reflect.DeepEqual(before.Tailnets, after.Tailnets) || before.Tailnet != after.Tailnet || before.App != after.App {
		t.Fatal("remove changed integration data")
	}
	if !reflect.DeepEqual(calls, []int{base.configured, base.dispatched, base.enabled}) {
		t.Fatal("remove mutated remote resources")
	}
	if w := publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 404 {
		t.Fatalf("missing: %d", w.Code)
	}
}
