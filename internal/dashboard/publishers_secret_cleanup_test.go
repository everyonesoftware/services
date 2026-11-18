package dashboard

import (
	"errors"
	"testing"

	"services/internal/githubapp"
)

func TestPublisherDeletionRetainsRecordWhenSecretCleanupFailsAndRetries(t *testing.T) {
	s, store, p, cookie, c := deletionTestServer(t)
	path := "/api/publishers/" + c.ID
	if w := publisherRequest(s, "POST", path+"/deletion/tailscale", deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated"), cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p.secretRemovalErr = errors.New("uncertain GitHub delete or readback")
	if w := publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 409 {
		t.Fatalf("cleanup failure: %d %s", w.Code, w.Body.String())
	}
	state, err := store.Load()
	if err != nil || len(state.Connections) != 1 || state.Connections[0].State != "disconnected" || p.secretRemovalCalls != 1 {
		t.Fatalf("failed cleanup lost retryable publisher: %+v %v", state.Connections, err)
	}
	p.secretRemovalErr = nil
	if w := publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 200 {
		t.Fatalf("retry: %d %s", w.Code, w.Body.String())
	}
	state, err = store.Load()
	if err != nil || len(state.Connections) != 0 || p.secretRemovalCalls != 2 {
		t.Fatal("retry did not reverify cleanup and remove saved publisher")
	}
}

func TestPublisherSecretCleanupRequiresAllDeletionPrerequisites(t *testing.T) {
	for _, scenario := range []string{"connected", "unconfirmed", "disable pending", "workflow restored", "successor", "changed trust", "repository inaccessible", "provider unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			s, store, p, cookie, c := deletionTestServer(t)
			path := "/api/publishers/" + c.ID
			if scenario != "unconfirmed" {
				if w := publisherRequest(s, "POST", path+"/deletion/tailscale", deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated"), cookie, ""); w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			}
			if scenario != "connected" {
				if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
					t.Fatal(w.Body.String())
				}
			}
			state, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "disable pending":
				state.Connections[0].RemoteDisablePending = true
			case "workflow restored":
				p.removal.Files = []githubapp.WorkflowFile{{Path: ".github/workflows/publish.yml", Before: "notify", After: "build"}}
			case "successor":
				next := state.Connections[0]
				next.ID = "successor"
				next.State = "verified"
				state.Connections = append(state.Connections, next)
			case "changed trust":
				trust := state.Tailnets["12"]
				trust.Audience = "changed"
				state.Tailnets["12"] = trust
			case "repository inaccessible":
				p.repositories = nil
			case "provider unavailable":
				s.publishers.provider = &removalReadyProvider{p}
			}
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			if w := publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 409 {
				t.Fatalf("unsafe removal: %d %s", w.Code, w.Body.String())
			}
			if p.secretRemovalCalls != 0 {
				t.Fatal("secret cleanup ran before deletion prerequisites")
			}
			saved, err := store.Load()
			if err != nil || len(saved.Connections) != len(state.Connections) {
				t.Fatal("blocked deletion lost saved publisher")
			}
		})
	}
}
