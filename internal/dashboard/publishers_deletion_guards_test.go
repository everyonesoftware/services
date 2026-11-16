package dashboard

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"services/internal/githubapp"
	"services/internal/publishers"
)

func TestPublisherDeletionSharedCredentialRequiresRepositoryScope(t *testing.T) {
	s, store, _, cookie, c := deletionTestServer(t)
	state, _ := store.Load()
	state.Tailnets["99"] = publishers.Tailnet{ClientID: "client-id", Audience: "other", Tags: "tag:other"}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	path := "/api/publishers/" + c.ID + "/deletion/"
	w := publisherRequest(s, "POST", path+"plan", map[string]any{}, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"shared":true`) {
		t.Fatalf("sharing not shown: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []map[string]any{{"confirmed": true, "clientId": "client-id", "scope": "dedicated"}, {"confirmed": false, "clientId": "client-id", "scope": "repository"}, {"confirmed": true, "clientId": "stale-id", "scope": "repository"}, {"confirmed": true, "clientId": "client-id", "scope": "unknown"}} {
		body["reviewFingerprint"] = deletionAttestationFromReview(t, s, c.ID, cookie, "repository")["reviewFingerprint"]
		if w = publisherRequest(s, "POST", path+"tailscale", body, cookie, ""); w.Code != 409 {
			t.Fatalf("unsafe attestation accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if w = publisherRequest(s, "POST", path+"tailscale", deletionAttestationFromReview(t, s, c.ID, cookie, "repository"), cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	saved, _ := store.Load()
	if saved.Tailnets["99"] != state.Tailnets["99"] {
		t.Fatal("shared tailnet config changed")
	}
	// The proof is intentionally not a provider claim or a server-managed revocation.
	var result map[string]any
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["tailscaleConfirmed"] != true {
		t.Fatal("manual confirmation not saved")
	}
}

func TestPublisherDeletionChangedTailnetInvalidatesManualConfirmation(t *testing.T) {
	s, store, _, cookie, c := deletionTestServer(t)
	path := "/api/publishers/" + c.ID
	w := publisherRequest(s, "POST", path+"/deletion/tailscale", deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated"), cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	state, _ := store.Load()
	tailnet := state.Tailnets["12"]
	tailnet.Audience = "different-trust"
	state.Tailnets["12"] = tailnet
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	if w = publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 409 {
		t.Fatalf("stale attestation accepted: %d", w.Code)
	}
	if w = publisherRequest(s, "POST", path+"/deletion/plan", map[string]any{}, cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"tailscaleConfirmed":false`) {
		t.Fatalf("stale proof displayed: %d %s", w.Code, w.Body.String())
	}
	state, _ = store.Load()
	if state.Connections[0].Deletion.TailscaleConfirmed {
		t.Fatal("stale proof not invalidated for resume")
	}
}

func TestPublisherDeletionProtectsActiveSuccessor(t *testing.T) {
	s, store, p, cookie, c := deletionTestServer(t)
	path := "/api/publishers/" + c.ID
	if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, ""); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	before, _ := store.Load()
	for _, action := range []string{"plan", "verify", "tailscale", "workflow"} {
		w := publisherRequest(s, "POST", path+"/deletion/"+action, map[string]any{}, cookie, "")
		if w.Code != 409 {
			t.Fatalf("successor cleanup %s: %d %s", action, w.Code, w.Body.String())
		}
	}
	after, _ := store.Load()
	if !reflect.DeepEqual(before, after) || p.removeCalls != 0 {
		t.Fatal("obsolete deletion changed successor")
	}
}

func TestPublisherDeletionRequiresSessionAndSameOrigin(t *testing.T) {
	s, _, p, cookie, c := deletionTestServer(t)
	for _, action := range []string{"plan", "workflow", "verify", "tailscale"} {
		path := "/api/publishers/" + c.ID + "/deletion/" + action
		if w := publisherRequest(s, "POST", path, map[string]any{}, nil, ""); w.Code != 401 {
			t.Fatalf("anonymous %s: %d", action, w.Code)
		}
		if w := publisherRequest(s, "POST", path, map[string]any{}, nil, c.Secret); w.Code != 403 {
			t.Fatalf("bearer %s: %d", action, w.Code)
		}
		req := httptest.NewRequest("POST", "https://localhost"+path, strings.NewReader(`{}`))
		req.AddCookie(cookie)
		req.Header.Set("Origin", "https://attacker.invalid")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("cross-origin %s: %d", action, w.Code)
		}
	}
	if p.removeCalls != 0 {
		t.Fatal("unauthorized mutation")
	}
}

func TestPublisherDeletionRejectsStaleApprovalAndRemoteFailure(t *testing.T) {
	s, store, p, cookie, c := deletionTestServer(t)
	p.removal.Files = []githubapp.WorkflowFile{{Path: "publish.yml", Before: "build and notify", After: "build"}}
	path := "/api/publishers/" + c.ID + "/deletion/"
	if w := publisherRequest(s, "POST", path+"workflow", map[string]any{"fingerprint": strings.Repeat("f", 64)}, cookie, ""); w.Code != 409 || p.removeCalls != 0 {
		t.Fatalf("stale approval accepted: %d", w.Code)
	}
	if w := publisherRequest(s, "POST", path+"tailscale", map[string]any{"confirmed": true, "clientId": "client-id", "scope": "dedicated"}, cookie, ""); w.Code != 409 {
		t.Fatalf("unmerged workflow skipped: %d", w.Code)
	}
	p.removeErr = githubapp.ErrConflict
	if w := publisherRequest(s, "POST", path+"workflow", map[string]any{"fingerprint": p.removal.Fingerprint}, cookie, ""); w.Code != 409 {
		t.Fatalf("failed PR reported success: %d", w.Code)
	}
	saved, _ := store.Load()
	if saved.Connections[0].Deletion == nil || saved.Connections[0].Deletion.PRURL != "" || saved.Connections[0].Deletion.TailscaleConfirmed {
		t.Fatal("uncertain PR lost intent or falsely completed")
	}
}
