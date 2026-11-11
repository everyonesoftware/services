package dashboard

import (
	"encoding/json"
	"services/internal/githubapp"
	"services/internal/publishers"
	"testing"
)

func TestPublisherNewRepositoryCannotInheritLegacyTailnet(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	state, _ := store.Load()
	// Simulate an old encrypted snapshot with only global federation.
	data, _ := json.Marshal(state)
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	delete(raw, "Tailnets")
	data, _ = json.Marshal(raw)
	state = publishers.Snapshot{}
	_ = json.Unmarshal(data, &state)
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	w := publisherRequest(s, "POST", "/api/publishers/plan", publisherSetupBody(), cookie, "")
	if w.Code != 403 {
		t.Fatalf("new repository inherited legacy federation: %d %s", w.Code, w.Body.String())
	}
}

func TestPublisherScopedTailnetsPreserveLegacy(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	state, _ := store.Load()
	legacy := state.Tailnet
	state.Tailnets = nil
	state.Connections = []publishers.Connection{{ID: "legacy", Repository: storedRepository(provider.repositories[0]), Configured: true}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	second := githubapp.Repository{ID: 13, FullName: "acme/finance", DefaultBranch: "main", InstallationID: 7}
	provider.repositories = append(provider.repositories, second)
	w := publisherRequest(s, "PUT", "/api/publishers/tailnet", map[string]any{"repository": second, "clientId": "finance-client", "audience": "finance-audience", "tags": "tag:finance"}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("scoped save: %d %s", w.Code, w.Body.String())
	}
	var status struct {
		Tailnets map[string]publishers.Tailnet
		Tailnet  publishers.Tailnet
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Tailnet != legacy || status.Tailnets["13"].ClientID != "finance-client" {
		t.Fatalf("status isolation: %+v", status)
	}
	body := map[string]any{"repository": second, "imageRepository": "ghcr.io/acme/finance"}
	w = publisherRequest(s, "POST", "/api/publishers/plan", body, cookie, "")
	var plan struct{ Variables map[string]string }
	_ = json.Unmarshal(w.Body.Bytes(), &plan)
	if w.Code != 200 || plan.Variables["TAILSCALE_CLIENT_ID"] != "finance-client" {
		t.Fatalf("plan isolation: %d %s", w.Code, w.Body.String())
	}
	w = publisherRequest(s, "PUT", "/api/publishers/tailnet", map[string]string{"clientId": "overwritten", "audience": "other", "tags": "tag:other"}, cookie, "")
	if w.Code != 409 {
		t.Fatalf("legacy overwrite: %d", w.Code)
	}
}

func TestPublisherLegacySettingsStayFrozenForDisconnectedSavedConnection(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	state, _ := store.Load()
	state.Tailnets = nil
	state.Connections = []publishers.Connection{{ID: "legacy", Repository: storedRepository(provider.repositories[0]), State: "disconnected"}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	w := publisherRequest(s, "PUT", "/api/publishers/tailnet", map[string]string{"clientId": "replacement", "audience": "replacement", "tags": "tag:replacement"}, cookie, "")
	if w.Code != 409 {
		t.Fatalf("saved legacy trust changed: %d %s", w.Code, w.Body.String())
	}
}
