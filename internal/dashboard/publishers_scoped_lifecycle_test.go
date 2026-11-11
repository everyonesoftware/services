package dashboard

import (
	"context"
	"encoding/json"
	"testing"

	"services/internal/githubapp"
	"services/internal/publishers"
)

type scopedLifecycleProvider struct {
	*lifecycleProvider
	configuredValues, recoveredValues map[int64]map[string]string
}

func (p *scopedLifecycleProvider) Configure(_ context.Context, _ githubapp.App, r githubapp.Repository, _ string, v map[string]string) error {
	p.configuredValues[r.ID] = v
	return nil
}
func (p *scopedLifecycleProvider) RecoverConfiguration(_ context.Context, _ githubapp.App, r githubapp.Repository, _ string, v map[string]string) error {
	p.recoveredValues[r.ID] = v
	return nil
}
func TestPublisherScopedCreateAndLegacyRecoveryRemainIsolated(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &scopedLifecycleProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}, configuredValues: map[int64]map[string]string{}, recoveredValues: map[int64]map[string]string{}}
	s.publishers.provider = p
	state, _ := store.Load()
	state.Tailnets = nil
	key, secret, err := keys.CreateKey("legacy", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	state.Connections = []publishers.Connection{{ID: "legacy-connection-id", Repository: storedRepository(base.repositories[0]), ImageRepository: "ghcr.io/acme/app", KeyID: key.ID, Secret: secret, State: "error"}}
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	finance := githubapp.Repository{ID: 13, FullName: "acme/finance", DefaultBranch: "main", InstallationID: 7}
	base.repositories = append(base.repositories, finance)
	// A second repo is denied by both plan and create despite the saved legacy connection.
	body := map[string]any{"repository": finance, "imageRepository": "ghcr.io/acme/finance"}
	for _, path := range []string{"/api/publishers/plan", "/api/publishers"} {
		w := publisherRequest(s, "POST", path, body, cookie, "")
		if w.Code != 403 {
			t.Fatalf("inherited global credential %s: %d", path, w.Code)
		}
	}
	w := publisherRequest(s, "PUT", "/api/publishers/tailnet", map[string]any{"repository": finance, "clientId": "finance-client", "audience": "finance-audience", "tags": "tag:finance"}, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = publisherRequest(s, "POST", "/api/publishers/plan", body, cookie, "")
	var plan struct{ Approval string }
	if err = json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	body["approval"] = plan.Approval
	w = publisherRequest(s, "POST", "/api/publishers", body, cookie, "")
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if p.configuredValues[13]["TAILSCALE_CLIENT_ID"] != "finance-client" {
		t.Fatalf("create used wrong trust: %+v", p.configuredValues)
	}
	w = publisherRequest(s, "POST", "/api/publishers/legacy-connection-id/recover", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("legacy recovery: %d %s", w.Code, w.Body.String())
	}
	if p.recoveredValues[12]["TAILSCALE_CLIENT_ID"] != "client-id" || p.recoveredValues[12]["TAILSCALE_AUDIENCE"] != "audience" {
		t.Fatalf("legacy recovery used new trust: %+v", p.recoveredValues)
	}
	w = publisherRequest(s, "POST", "/api/publishers/legacy-connection-id/rotate", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("legacy rotation: %d %s", w.Code, w.Body.String())
	}
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Tailnet.ClientID != "client-id" || state.Tailnets["13"].ClientID != "finance-client" {
		t.Fatalf("lifecycle changed trust: %+v %+v", state.Tailnet, state.Tailnets)
	}
	// Configured repo trust cannot be silently replaced, including by scoped clients.
	w = publisherRequest(s, "PUT", "/api/publishers/tailnet", map[string]any{"repository": finance, "clientId": "replacement", "audience": "replacement", "tags": "tag:replacement"}, cookie, "")
	if w.Code != 409 {
		t.Fatalf("configured trust overwritten: %d", w.Code)
	}
}
