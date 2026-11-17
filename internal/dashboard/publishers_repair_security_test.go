package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"services/internal/githubapp"
	"services/internal/publishers"
)

func TestPublisherRepairRequiresSessionAndSameOrigin(t *testing.T) {
	s, _, p, cookie := repairServer(t)
	_, token, err := s.authStore.CreateKey("api", []string{"services:read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"repair/plan", "repair"} {
		path := "/api/publishers/repair/" + suffix
		for _, tc := range []struct {
			token  string
			status int
		}{{"", 401}, {token, 403}} {
			w := publisherRequest(s, "POST", path, map[string]any{}, nil, tc.token)
			if w.Code != tc.status {
				t.Fatalf("%s auth=%d %s", suffix, w.Code, w.Body.String())
			}
		}
		r := httptest.NewRequest("POST", "https://localhost"+path, strings.NewReader(`{}`))
		r.AddCookie(cookie)
		r.Header.Set("Origin", "https://evil.invalid")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("cross-origin %s=%d", suffix, w.Code)
		}
	}
	if p.applied != 0 || p.dispatched != 0 {
		t.Fatal("unauthorized mutation")
	}
}

func TestPublisherRepairStaleApprovalNeverCreatesKeyOrWrites(t *testing.T) {
	for _, change := range []string{"provider", "identity", "image", "key", "configured", "state", "tailnet", "endpoint", "rename", "replacement"} {
		t.Run(change, func(t *testing.T) {
			s, store, p, cookie := repairServer(t)
			fingerprint := reviewedRepair(t, s, cookie)
			state, _ := store.Load()
			c := &state.Connections[0]
			switch change {
			case "provider":
				p.plan.Fingerprint = strings.Repeat("b", 64)
			case "identity":
				c.ID = "replacement"
			case "image":
				c.ImageRepository = "ghcr.io/acme/other"
			case "key":
				c.KeyID = "different"
			case "configured":
				c.Configured = true
			case "state":
				c.State = "disconnected"
			case "tailnet":
				state.Tailnets["12"] = publishers.Tailnet{ClientID: "changed", Audience: "audience", Tags: "tag:publisher"}
			case "endpoint":
				s.publishers.endpoint = "https://changed.example/api/images/published"
			case "rename":
				p.repositories[0].FullName = "acme/renamed"
			case "replacement":
				p.repositories[0].ID = 99
			}
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			w := publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
			if w.Code < 400 || p.applied != 0 || p.dispatched != 0 {
				t.Fatalf("stale approval=%d %s", w.Code, w.Body.String())
			}
			keys, _ := s.authStore.ListKeys()
			if len(keys) != 0 {
				t.Fatal("stale review created key")
			}
		})
	}
}

func TestPublisherRepairLifecycleGuards(t *testing.T) {
	for _, guard := range []string{"verified", "awaiting_callback", "revocation", "historical_revocation", "successor", "tailnet", "permissions", "workflow"} {
		t.Run(guard, func(t *testing.T) {
			s, store, p, cookie := repairServer(t)
			state, _ := store.Load()
			c := &state.Connections[0]
			switch guard {
			case "verified", "awaiting_callback":
				c.State = guard
			case "revocation":
				c.Deletion = &publishers.Deletion{TailscaleConfirmed: true}
			case "historical_revocation":
				c.Deletion = &publishers.Deletion{TailscaleRevocationAttested: true}
			case "successor":
				other := *c
				other.ID = "successor"
				other.State = "error"
				state.Connections = append(state.Connections, other)
			case "tailnet":
				state.Tailnets["12"] = publishers.Tailnet{}
			case "permissions":
				s.publishers.provider = &repairRestrictedProvider{repairProvider: p, deny: true}
			case "workflow":
				s.publishers.provider = &repairRestrictedProvider{repairProvider: p, unsupported: true}
			}
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"repair/plan", "repair"} {
				w := publisherRequest(s, "POST", "/api/publishers/repair/"+suffix, map[string]any{}, cookie, "")
				if w.Code < 400 {
					t.Fatalf("%s allowed %s: %d", guard, suffix, w.Code)
				}
			}
			keys, _ := s.authStore.ListKeys()
			if len(keys) != 0 || p.applied != 0 {
				t.Fatal("guard allowed mutation")
			}
		})
	}
}

type repairRestrictedProvider struct {
	*repairProvider
	deny, unsupported bool
}

func (p *repairRestrictedProvider) VerifyRepositoryPermissions(ctx context.Context, a githubapp.App, r githubapp.Repository) error {
	if p.deny {
		return errors.New("installation grant missing")
	}
	return nil
}
func (p *repairRestrictedProvider) Inspect(context.Context, githubapp.App, githubapp.Repository) (githubapp.Inspection, error) {
	return githubapp.Inspection{Supported: !p.unsupported}, nil
}

func TestPublisherRepairReusesSavedCredentialAfterUncertainFailure(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprint(saved), func(t *testing.T) {
			s, store, p, cookie := repairServer(t)
			if saved {
				key, secret, err := s.authStore.CreateKey("existing", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
				if err != nil {
					t.Fatal(err)
				}
				state, _ := store.Load()
				c := &state.Connections[0]
				c.KeyID = key.ID
				c.Secret = secret
				c.Configured = true
				c.VerifiedAt = "old"
				c.ChallengeHash = digestID(strings.Repeat("c", 43))
				c.ChallengeExpires = time.Now().Add(time.Hour).Unix()
				if err := store.Save(state); err != nil {
					t.Fatal(err)
				}
			}
			p.applyErr = errors.New("uncertain network failure")
			p.beforeApply = func() {
				state, _ := store.Load()
				c := state.Connections[0]
				if c.Configured || c.VerifiedAt != "" || c.ChallengeHash != "" || c.ChallengeExpires != 0 || !c.ChallengeUsed {
					t.Fatal("old proof remained usable")
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				fingerprint := reviewedRepair(t, s, cookie)
				w := publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
				if w.Code != 502 {
					t.Fatalf("uncertain apply=%d %s", w.Code, w.Body.String())
				}
				state, _ := store.Load()
				if state.Connections[0].State != "error" || state.Connections[0].Configured || !strings.Contains(state.Connections[0].Error, "fresh repair plan") {
					t.Fatal("failure not retryable")
				}
				keys, _ := s.authStore.ListKeys()
				if len(keys) != 1 {
					t.Fatalf("retry keys=%d", len(keys))
				}
			}
			delivered := p.delivered
			p.applyErr = nil
			fingerprint := reviewedRepair(t, s, cookie)
			w := publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
			if w.Code != 200 || p.delivered != delivered || p.enabled != 0 {
				t.Fatalf("retry failed=%d %s", w.Code, w.Body.String())
			}
			keys, _ := s.authStore.ListKeys()
			if len(keys) != 1 {
				t.Fatal("retry duplicated key")
			}
		})
	}
}

func TestPublisherRepairRevokedCredentialGetsFreshKey(t *testing.T) {
	s, store, p, cookie := repairServer(t)
	key, secret, err := s.authStore.CreateKey("revoked", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load()
	c := &state.Connections[0]
	c.KeyID = key.ID
	c.Secret = secret
	c.State = "disconnected"
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := s.authStore.RevokeKey(key.ID); err != nil {
		t.Fatal(err)
	}
	fingerprint := reviewedRepair(t, s, cookie)
	w := publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("revoked repair=%d %s", w.Code, w.Body.String())
	}
	if _, err := s.authStore.AuthenticateKey(secret); err == nil {
		t.Fatal("revoked key restored")
	}
	state, _ = store.Load()
	if state.Connections[0].KeyID == key.ID || p.delivered == secret {
		t.Fatal("revoked credential reused")
	}
}

func TestPublisherRepairUsesFreshStableRepository(t *testing.T) {
	s, store, p, cookie := repairServer(t)
	p.repositories[0].FullName = "acme/renamed"
	p.repositories[0].DefaultBranch = "trunk"
	fingerprint := reviewedRepair(t, s, cookie)
	w := publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("rename repair=%d %s", w.Code, w.Body.String())
	}
	state, _ := store.Load()
	c := state.Connections[0]
	if c.Repository.FullName != "acme/renamed" || c.Repository.DefaultBranch != "trunk" {
		t.Fatal("stale repository saved")
	}
	body := map[string]string{"connectionId": c.ID, "challenge": p.challenge, "sourceRepository": c.Repository.FullName, "ref": "refs/heads/trunk", "runId": "42"}
	w = publisherRequest(s, "POST", "/api/publishers/callback", body, nil, c.Secret)
	if w.Code != 200 || p.enabled != 1 {
		t.Fatalf("renamed callback=%d %s", w.Code, w.Body.String())
	}
}

func TestPublisherRepairInvalidatesRotationProofAndRetiresPreviousOnlyAfterCallback(t *testing.T) {
	s, store, p, cookie := repairServer(t)
	previous, previousSecret, err := s.authStore.CreateKey("previous", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	current, currentSecret, err := s.authStore.CreateKey("current", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	oldChallenge := strings.Repeat("z", 43)
	state, _ := store.Load()
	c := &state.Connections[0]
	c.KeyID = current.ID
	c.Secret = currentSecret
	c.PreviousKeyID = previous.ID
	c.PreviousSecret = previousSecret
	c.Configured = true
	c.ChallengeHash = digestID(oldChallenge)
	c.ChallengeExpires = time.Now().Add(time.Hour).Unix()
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	p.applyErr = errors.New("uncertain delivery")
	fingerprint := reviewedRepair(t, s, cookie)
	w := publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
	if w.Code != 502 {
		t.Fatal(w.Body.String())
	}
	callback := map[string]string{"connectionId": "repair", "challenge": oldChallenge, "sourceRepository": "acme/app", "ref": "refs/heads/main", "runId": "42"}
	for _, secret := range []string{previousSecret, currentSecret} {
		w = publisherRequest(s, "POST", "/api/publishers/callback", callback, nil, secret)
		if w.Code != 403 || p.enabled != 0 {
			t.Fatalf("old proof accepted=%d %s", w.Code, w.Body.String())
		}
	}
	p.applyErr = nil
	fingerprint = reviewedRepair(t, s, cookie)
	w = publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	callback["challenge"] = p.challenge
	w = publisherRequest(s, "POST", "/api/publishers/callback", callback, nil, previousSecret)
	if w.Code != 403 || p.enabled != 0 {
		t.Fatal("previous rotation key verified repair")
	}
	w = publisherRequest(s, "POST", "/api/publishers/callback", callback, nil, currentSecret)
	if w.Code != 200 || p.enabled != 1 {
		t.Fatalf("fresh proof failed=%d %s", w.Code, w.Body.String())
	}
	if _, err := s.authStore.AuthenticateKey(previousSecret); err == nil {
		t.Fatal("previous key was not retired")
	}
}

func TestPublisherRepairDispatchFailureRetainsVerifiedConfiguration(t *testing.T) {
	s, store, p, cookie := repairServer(t)
	p.dispatchErr = errors.New("uncertain dispatch")
	fingerprint := reviewedRepair(t, s, cookie)
	w := publisherRequest(s, "POST", "/api/publishers/repair/repair", map[string]string{"fingerprint": fingerprint}, cookie, "")
	if w.Code != 502 {
		t.Fatal(w.Body.String())
	}
	state, _ := store.Load()
	c := state.Connections[0]
	if !c.Configured || c.State != "error" || c.Secret == "" || p.enabled != 0 {
		t.Fatalf("dispatch failure status=%+v", connectionView(c))
	}
	p.dispatchErr = nil
	w = publisherRequest(s, "POST", "/api/publishers/repair/test", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	keys, _ := s.authStore.ListKeys()
	if len(keys) != 1 {
		t.Fatal("test retry minted key")
	}
}

func TestPublisherRepairRejectsUnreviewedBodies(t *testing.T) {
	s, _, p, cookie := repairServer(t)
	for _, body := range []any{map[string]any{}, map[string]any{"fingerprint": "bad"}, map[string]any{"fingerprint": strings.Repeat("a", 64)}, map[string]any{"fingerprint": "x", "secret": "no"}} {
		w := publisherRequest(s, "POST", "/api/publishers/repair/repair", body, cookie, "")
		if w.Code < 400 || p.applied != 0 {
			t.Fatalf("invalid review=%d", w.Code)
		}
	}
	w := publisherRequest(s, "POST", "/api/publishers/repair/repair/plan", map[string]any{"secret": "no"}, cookie, "")
	if w.Code != 400 {
		t.Fatal("plan accepted unknown fields")
	}
	keys, _ := s.authStore.ListKeys()
	if len(keys) != 0 {
		t.Fatal("invalid approval minted key")
	}
}
