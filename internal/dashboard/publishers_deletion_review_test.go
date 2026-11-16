package dashboard

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"services/internal/githubapp"
)

func deletionAttestationFromReview(t *testing.T, s *Server, id string, cookie *http.Cookie, scope string) map[string]any {
	t.Helper()
	w := publisherRequest(s, "POST", "/api/publishers/"+id+"/deletion/plan", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("review: %d %s", w.Code, w.Body.String())
	}
	var plan struct {
		Review    string `json:"tailscaleReview"`
		Tailscale struct {
			ClientID string `json:"clientId"`
		} `json:"tailscale"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"confirmed": true, "clientId": plan.Tailscale.ClientID, "scope": scope}
	if plan.Review != "" {
		body["reviewFingerprint"] = plan.Review
	}
	return body
}
func TestPublisherDeletionRejectsAttestationFromOldReview(t *testing.T) {
	s, store, _, cookie, c := deletionTestServer(t)
	body := deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated")
	state, _ := store.Load()
	tailnet := state.Tailnets["12"]
	tailnet.Audience = "new-audience-not-reviewed"
	state.Tailnets["12"] = tailnet
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	w := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/deletion/tailscale", body, cookie, "")
	if w.Code != 409 {
		t.Fatalf("old review attested new trust: %d %s", w.Code, w.Body.String())
	}
}
func TestPublisherDeletionRevocationHistorySurvivesWorkflowRestore(t *testing.T) {
	s, _, p, cookie, c := deletionTestServer(t)
	path := "/api/publishers/" + c.ID
	body := deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated")
	if w := publisherRequest(s, "POST", path+"/deletion/tailscale", body, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p.removal.Files = []githubapp.WorkflowFile{{Path: "publish.yml", Before: "restored", After: "clean"}}
	if w := publisherRequest(s, "POST", path+"/deletion/plan", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "POST", path+"/reconnect", map[string]any{}, cookie, ""); w.Code != 409 {
		t.Fatalf("lost revocation safeguard: %d %s", w.Code, w.Body.String())
	}
	status := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	if !strings.Contains(status.Body.String(), `"tailscaleRevocationAttested":true`) {
		t.Fatalf("revocation history lost: %s", status.Body.String())
	}
}
func TestPublisherDeletionResolvesChangedDefaultBranch(t *testing.T) {
	s, _, p, cookie, c := deletionTestServer(t)
	p.repositories[0].DefaultBranch = "trunk"
	w := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/deletion/plan", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("new default branch blocked: %d %s", w.Code, w.Body.String())
	}
	if p.plannedRepository.DefaultBranch != "trunk" {
		t.Fatalf("inspected stale default branch: %+v", p.plannedRepository)
	}
}

func TestPublisherDeletionResolvesRenamedRepository(t *testing.T) {
	s, store, p, cookie, c := deletionTestServer(t)
	p.repositories[0].FullName = "acme/renamed-app"
	p.repositories[0].DefaultBranch = "trunk"
	path := "/api/publishers/" + c.ID
	p.removal.Files = []githubapp.WorkflowFile{{Path: ".github/workflows/publish.yml", Before: "build and notify", After: "build"}}
	w := publisherRequest(s, "POST", path+"/deletion/plan", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("renamed repository blocked: %d %s", w.Code, w.Body.String())
	}
	if p.plannedRepository != p.repositories[0] {
		t.Fatalf("inspected stale repository: %+v", p.plannedRepository)
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Connections[0].Repository != storedRepository(p.repositories[0]) || saved.Connections[0].ImageRepository != c.ImageRepository {
		t.Fatalf("rename was not saved independently of image identity: %+v", saved.Connections[0])
	}
	if w = publisherRequest(s, "POST", path+"/deletion/workflow", map[string]any{"fingerprint": p.removal.Fingerprint}, cookie, ""); w.Code != 201 {
		t.Fatalf("cleanup PR for renamed repository: %d %s", w.Code, w.Body.String())
	}
	if p.removedRepository != p.repositories[0] || !strings.Contains(w.Body.String(), "https://github.com/acme/renamed-app/pull/42") {
		t.Fatalf("cleanup targeted stale repository: %+v %s", p.removedRepository, w.Body.String())
	}
	p.removal.Files = []githubapp.WorkflowFile{}
	if w = publisherRequest(s, "POST", path+"/deletion/verify", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatalf("verify renamed cleanup: %d %s", w.Code, w.Body.String())
	}
	if w = publisherRequest(s, "POST", path+"/deletion/tailscale", deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated"), cookie, ""); w.Code != 200 {
		t.Fatalf("confirm renamed cleanup: %d %s", w.Code, w.Body.String())
	}
	if w = publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatalf("disconnect renamed publisher: %d %s", w.Code, w.Body.String())
	}
	if w = publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 200 {
		t.Fatalf("delete renamed publisher: %d %s", w.Code, w.Body.String())
	}
	saved, err = store.Load()
	if err != nil || len(saved.Connections) != 0 {
		t.Fatalf("renamed publisher retained: %+v %v", saved.Connections, err)
	}
}

func TestPublisherDeletionRejectsReplacementRepositoryOrInstallation(t *testing.T) {
	for _, changed := range []string{"repository", "installation"} {
		t.Run(changed, func(t *testing.T) {
			s, _, p, cookie, c := deletionTestServer(t)
			if changed == "repository" {
				p.repositories[0].ID++
			} else {
				p.repositories[0].InstallationID++
			}
			w := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/deletion/plan", map[string]any{}, cookie, "")
			if w.Code != 409 || p.plannedRepository.ID != 0 || p.removeCalls != 0 {
				t.Fatalf("replacement identity accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPublisherDeletionRenameInvalidatesOldAttestation(t *testing.T) {
	s, _, p, cookie, c := deletionTestServer(t)
	body := deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated")
	p.repositories[0].FullName = "acme/renamed-app"
	w := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/deletion/tailscale", body, cookie, "")
	if w.Code != 409 {
		t.Fatalf("old-name attestation accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestPublisherDeletionRevocationHistoryBlocksLateCallback(t *testing.T) {
	s, _, p, cookie, c := deletionTestServer(t)
	body := deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated")
	if w := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/deletion/tailscale", body, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w := publisherRequest(s, "POST", "/api/publishers/callback", callbackBody(c, p.challenge), nil, c.Secret)
	if w.Code != 403 || p.enabled != 0 {
		t.Fatalf("late callback enabled revoked integration: %d %s", w.Code, w.Body.String())
	}
}
