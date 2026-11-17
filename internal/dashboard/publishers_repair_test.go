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

type repairProvider struct {
	*publisherProviderFake
	plan        githubapp.ConfigurationPlan
	applied     int
	applyErr    error
	beforeApply func()
	delivered   string
}

func (p *repairProvider) PlanConfiguration(context.Context, githubapp.App, githubapp.Repository, map[string]string) (githubapp.ConfigurationPlan, error) {
	return p.plan, nil
}
func (p *repairProvider) ApplyConfigurationRepair(_ context.Context, _ githubapp.App, _ githubapp.Repository, secret string, _ map[string]string, fingerprint string) error {
	if fingerprint != p.plan.Fingerprint {
		return errors.New("provider fingerprint lost")
	}
	p.applied++
	p.delivered = secret
	if p.beforeApply != nil {
		p.beforeApply()
	}
	return p.applyErr
}
func repairServer(t *testing.T) (*Server, *publishers.Store, *repairProvider, *http.Cookie) {
	t.Helper()
	s, _, store, p, cookie := publisherServer(t)
	rp := &repairProvider{publisherProviderFake: p, plan: githubapp.ConfigurationPlan{SecretExists: true, Fingerprint: strings.Repeat("a", 64), Variables: []githubapp.ConfigurationVariable{{Name: "TAILSCALE_CLIENT_ID", Exists: true, CurrentValue: "old", DesiredValue: "client-id"}}}}
	s.publishers.provider = rp
	state, _ := store.Load()
	state.Connections = []publishers.Connection{{ID: "repair", Repository: storedRepository(p.repositories[0]), ImageRepository: "ghcr.io/acme/app", State: "error"}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	return s, store, rp, cookie
}
func reviewedRepair(t *testing.T, s *Server, cookie *http.Cookie) string {
	t.Helper()
	w := publisherRequest(s, "POST", "/api/publishers/repair/repair/plan", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("plan=%d %s", w.Code, w.Body.String())
	}
	var response struct {
		Configuration githubapp.ConfigurationPlan `json:"configuration"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Configuration.Fingerprint) != 64 {
		t.Fatal("missing review fingerprint")
	}
	return response.Configuration.Fingerprint
}
func TestPublisherReviewedRepairMissingCredential(t *testing.T) {
	s, store, p, cookie := repairServer(t)
	fingerprint := reviewedRepair(t, s, cookie)
	keys, _ := s.authStore.ListKeys()
	if len(keys) != 0 || p.applied != 0 {
		t.Fatal("plan mutated credentials/configuration")
	}
	p.beforeApply = func() {
		state, _ := store.Load()
		c := state.Connections[0]
		if c.Secret == "" || c.Configured || c.ChallengeHash != "" || c.VerifiedAt != "" {
			t.Fatalf("unsafe saved intent: %+v", connectionView(c))
		}
	}
	w := publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("repair=%d %s", w.Code, w.Body.String())
	}
	state, _ := store.Load()
	c := state.Connections[0]
	if !c.Configured || c.State != "awaiting_callback" || p.enabled != 0 || p.dispatched != 1 {
		t.Fatalf("repair status: %+v", connectionView(c))
	}
	if strings.Contains(w.Body.String(), c.Secret) {
		t.Fatal("secret leak")
	}
	body := map[string]string{"connectionId": c.ID, "challenge": p.challenge, "sourceRepository": c.Repository.FullName, "ref": "refs/heads/main", "runId": "42"}
	w = publisherRequest(s, "POST", "/api/publishers/callback", body, nil, c.Secret)
	if w.Code != 200 || p.enabled != 1 {
		t.Fatalf("callback=%d %s", w.Code, w.Body.String())
	}
}

func TestPublisherReviewedRepairRejectsReadyConnection(t *testing.T) {
	s, store, p, cookie := repairServer(t)
	state, _ := store.Load()
	state.Connections[0].State = "configured"
	state.Connections[0].Configured = true
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	w := publisherRequest(s, "POST", "/api/publishers/repair/repair/plan", map[string]any{}, cookie, "")
	if w.Code != 409 || p.applied != 0 {
		t.Fatalf("ready connection repair=%d %s", w.Code, w.Body.String())
	}
}

func TestPublisherRepairGuidanceForMissingCredential(t *testing.T) {
	s, _, _, cookie := repairServer(t)
	for _, action := range []string{"test", "recover"} {
		w := publisherRequest(s, "POST", "/api/publishers/repair/"+action, map[string]any{}, cookie, "")
		if w.Code != 409 || !strings.Contains(strings.ToLower(w.Body.String()), "reviewed repair") {
			t.Fatalf("%s guidance=%d %s", action, w.Code, w.Body.String())
		}
	}
}

func TestPublisherReviewedRepairRejectsWrongScopeCredential(t *testing.T) {
	s, store, p, cookie := repairServer(t)
	key, secret, err := s.authStore.CreateKey("wrong", []string{"services:read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load()
	state.Connections[0].KeyID = key.ID
	state.Connections[0].Secret = secret
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	fingerprint := reviewedRepair(t, s, cookie)
	w := publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
	if w.Code != 409 || p.applied != 0 {
		t.Fatalf("wrong scope delivered=%d %s", w.Code, w.Body.String())
	}
}

func TestPublisherRepairRetiresUnavailableSavedCredential(t *testing.T) {
	s, store, _, cookie := repairServer(t)
	key, secret, err := s.authStore.CreateKey("lost secret", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load()
	state.Connections[0].KeyID = key.ID
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	fingerprint := reviewedRepair(t, s, cookie)
	w := publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err := s.authStore.AuthenticateKey(secret); err == nil {
		t.Fatal("unavailable old key left active and unmanaged")
	}
	keys, _ := s.authStore.ListKeys()
	if len(keys) != 1 {
		t.Fatalf("active credentials=%d", len(keys))
	}
}

func TestPublisherRepairCredentialStatus(t *testing.T) {
	s, keys, store, _, cookie := publisherServer(t)
	key, secret, err := keys.CreateKey("saved", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load()
	state.Connections = []publishers.Connection{{ID: "repair", Repository: storedRepository(publisherSetupBody()["repository"].(githubapp.Repository)), ImageRepository: "ghcr.io/acme/app", State: "error", KeyID: key.ID, Secret: secret}}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	for _, available := range []bool{true, false} {
		if !available {
			if err := keys.RevokeKey(key.ID); err != nil {
				t.Fatal(err)
			}
		}
		w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var response struct {
			Connections []map[string]any `json:"connections"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		c := response.Connections[0]
		if c["configured"] != false || c["credentialAvailable"] != available {
			t.Fatalf("credential status: %s", w.Body.String())
		}
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("secret leaked")
		}
	}
}
