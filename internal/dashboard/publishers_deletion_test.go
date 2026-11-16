package dashboard

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"services/internal/githubapp"
)

type deletionProvider struct {
	*lifecycleProvider
	removal           githubapp.WorkflowPlan
	removalErr        error
	removeErr         error
	removeCalls       int
	plannedRepository githubapp.Repository
}

func (p *deletionProvider) PlanWorkflowRemoval(_ context.Context, _ githubapp.App, repo githubapp.Repository) (githubapp.WorkflowPlan, error) {
	p.plannedRepository = repo
	return p.removal, p.removalErr
}
func (p *deletionProvider) RemoveWorkflow(_ context.Context, _ githubapp.App, _ githubapp.Repository, fingerprint string) (string, error) {
	p.removeCalls++
	if fingerprint != p.removal.Fingerprint {
		return "", githubapp.ErrConflict
	}
	return "https://github.com/acme/app/pull/42", p.removeErr
}
func TestPublisherDeletionReviewDoesNotMutateRemoteOrRevokeKeys(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &deletionProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}, removal: githubapp.WorkflowPlan{Supported: true, Problems: []string{}, Files: []githubapp.WorkflowFile{{Path: ".github/workflows/publish.yml", Before: "build and notify", After: "build"}}, Fingerprint: strings.Repeat("a", 64)}}
	s.publishers.provider = p
	c := connectionForTest(t, s, store, cookie)
	w := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/deletion/plan", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("review: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Workflow  githubapp.WorkflowPlan `json:"workflow"`
		Tailscale struct {
			ClientID       string `json:"clientId"`
			CredentialsURL string `json:"credentialsUrl"`
		} `json:"tailscale"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Workflow.Files) != 1 || result.Tailscale.ClientID != "client-id" || result.Tailscale.CredentialsURL != "https://console.tailscale.com/admin/settings/trust-credentials" {
		t.Fatalf("missing review: %s", w.Body.String())
	}
	if p.removeCalls != 0 || p.disabled != 0 {
		t.Fatal("review changed remote state")
	}
	if _, err := keys.AuthenticateKey(c.Secret); err != nil {
		t.Fatal("review revoked credential", err)
	}
	if strings.Contains(w.Body.String(), c.Secret) {
		t.Fatal("review disclosed key")
	}
}

func TestPublisherDeletionPersistsReviewAndManualRevocationBeforeRemoval(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &deletionProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}, removal: githubapp.WorkflowPlan{Supported: true, Problems: []string{}, Files: []githubapp.WorkflowFile{{Path: ".github/workflows/publish.yml", Before: "build and notify", After: "build"}}, Fingerprint: strings.Repeat("b", 64)}}
	s.publishers.provider = p
	c := connectionForTest(t, s, store, cookie)
	path := "/api/publishers/" + c.ID
	w := publisherRequest(s, "POST", path+"/deletion/workflow", map[string]any{"fingerprint": p.removal.Fingerprint}, cookie, "")
	if w.Code != 201 {
		t.Fatalf("cleanup PR: %d %s", w.Code, w.Body.String())
	}
	if p.removeCalls != 1 || !strings.Contains(w.Body.String(), "/pull/42") {
		t.Fatal("cleanup PR was not recorded")
	}
	if w = publisherRequest(s, "POST", path+"/deletion/verify", map[string]any{}, cookie, ""); w.Code != 409 {
		t.Fatalf("unmerged workflow accepted: %d %s", w.Code, w.Body.String())
	}
	p.removal.Files = []githubapp.WorkflowFile{}
	if w = publisherRequest(s, "POST", path+"/deletion/verify", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatalf("verify: %d %s", w.Code, w.Body.String())
	}
	if w = publisherRequest(s, "POST", path+"/deletion/tailscale", deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated"), cookie, ""); w.Code != 200 {
		t.Fatalf("manual revoke confirmation: %d %s", w.Code, w.Body.String())
	}
	status := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	if !strings.Contains(status.Body.String(), `"tailscaleConfirmed":true`) || !strings.Contains(status.Body.String(), `"workflowVerified":true`) {
		t.Fatalf("resume state missing: %s", status.Body.String())
	}
	if _, err := keys.AuthenticateKey(c.Secret); err != nil {
		t.Fatal("manual Tailscale confirmation silently revoked Services key")
	}
	if w = publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 200 {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	after, _ := store.Load()
	if len(after.Connections) != 0 {
		t.Fatal("connection retained after verified removal")
	}
}
