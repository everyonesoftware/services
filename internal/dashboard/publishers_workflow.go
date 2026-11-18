package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"services/internal/githubapp"
	"services/internal/publishers"
)

type publisherWorkflowProvider interface {
	PlanWorkflow(context.Context, githubapp.App, githubapp.Repository) (githubapp.WorkflowPlan, error)
	InstallWorkflow(context.Context, githubapp.App, githubapp.Repository, string) (string, error)
}

// Approval binds the review to the exact installation, image, endpoint, tailnet
// settings, workflow snapshot, and optional provider configuration snapshot. It
// contains no credentials; omitting configuration preserves legacy approvals.
func publisherApproval(repo githubapp.Repository, image string, variables map[string]string, workflow *githubapp.WorkflowPlan, configuration ...string) string {
	fingerprint := ""
	if workflow != nil {
		fingerprint = workflow.Fingerprint
	}
	configurationFingerprint := ""
	if len(configuration) > 0 {
		configurationFingerprint = configuration[0]
	}
	data, _ := json.Marshal(struct {
		Repository    githubapp.Repository
		Image         string
		Variables     map[string]string
		Workflow      string
		Configuration string `json:",omitempty"`
	}{repo, image, variables, fingerprint, configurationFingerprint})
	return digestID(string(data))
}
func (m *publisherIntegration) workflowPlan(ctx context.Context, state publishers.Snapshot, repo githubapp.Repository) (*githubapp.WorkflowPlan, error) {
	provider, ok := m.provider.(publisherWorkflowProvider)
	if !ok {
		return nil, nil
	}
	plan, err := provider.PlanWorkflow(ctx, appCredentials(state.App), repo)
	return &plan, err
}
func (s *Server) publisherInstallWorkflow(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		Repository      githubapp.Repository `json:"repository"`
		ImageRepository string               `json:"imageRepository"`
		Fingerprint     string               `json:"fingerprint"`
	}
	if !authDecode(w, r, &input) {
		return
	}
	if len(input.Fingerprint) != 64 {
		http.Error(w, "Review a fresh workflow plan before approving installation", 400)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		http.Error(w, "Publisher storage unavailable", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	repo, err := m.resolve(ctx, &state, publisherSetupRequest{Repository: input.Repository, ImageRepository: input.ImageRepository})
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	provider, ok := m.provider.(publisherWorkflowProvider)
	if !ok {
		http.Error(w, "Workflow installation unavailable", 503)
		return
	}
	prURL, err := provider.InstallWorkflow(ctx, appCredentials(state.App), repo, input.Fingerprint)
	if err != nil {
		if errors.Is(err, githubapp.ErrConflict) {
			http.Error(w, "Workflow changed since review or the setup branch conflicts. Request a new plan before retrying.", 409)
		} else {
			http.Error(w, "Setup pull request could not be verified. Review GitHub and refresh the plan before retrying.", 502)
		}
		return
	}
	authJSON(w, 201, map[string]string{"prUrl": prURL})
}
