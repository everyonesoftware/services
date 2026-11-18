package dashboard

import (
	"context"
	"net/http"
	"services/internal/githubapp"
	"strings"
	"testing"
)

type removalReadyProvider struct{ publisherProvider }

type secretRemovalReadyProvider struct{ *removalReadyProvider }

func (p *secretRemovalReadyProvider) RemovePublisherSecret(context.Context, githubapp.App, githubapp.Repository) error {
	return nil
}

func (p *removalReadyProvider) PlanWorkflowRemoval(context.Context, githubapp.App, githubapp.Repository) (githubapp.WorkflowPlan, error) {
	return githubapp.WorkflowPlan{Supported: true, Problems: []string{}, Files: []githubapp.WorkflowFile{}, Fingerprint: strings.Repeat("d", 64)}, nil
}
func (p *removalReadyProvider) RemoveWorkflow(context.Context, githubapp.App, githubapp.Repository, string) (string, error) {
	return "", githubapp.ErrConflict
}
func confirmDeletionForTest(t *testing.T, s *Server, id string, cookie *http.Cookie) {
	t.Helper()
	s.publishers.provider = &secretRemovalReadyProvider{&removalReadyProvider{s.publishers.provider}}
	if w := publisherRequest(s, "POST", "/api/publishers/"+id+"/deletion/tailscale", deletionAttestationFromReview(t, s, id, cookie, "dedicated"), cookie, ""); w.Code != 200 {
		t.Fatalf("confirm deletion prerequisites: %d %s", w.Code, w.Body.String())
	}
}
