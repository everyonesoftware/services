package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"services/internal/githubapp"
	"services/internal/publishers"
)

const tailscaleCredentialsURL = "https://console.tailscale.com/admin/settings/trust-credentials"

type publisherRemovalProvider interface {
	PlanWorkflowRemoval(context.Context, githubapp.App, githubapp.Repository) (githubapp.WorkflowPlan, error)
	RemoveWorkflow(context.Context, githubapp.App, githubapp.Repository, string) (string, error)
}
type publisherSecretRemovalProvider interface {
	RemovePublisherSecret(context.Context, githubapp.App, githubapp.Repository) error
}
type publisherDeletionTailnet struct {
	publishers.Tailnet
	Shared         bool   `json:"shared"`
	CredentialsURL string `json:"credentialsUrl"`
}
type publisherDeletionPlan struct {
	Workflow           githubapp.WorkflowPlan   `json:"workflow"`
	PRURL              string                   `json:"prUrl"`
	WorkflowVerified   bool                     `json:"workflowVerified"`
	TailscaleConfirmed bool                     `json:"tailscaleConfirmed"`
	TailscaleReview    string                   `json:"tailscaleReview"`
	Tailscale          publisherDeletionTailnet `json:"tailscale"`
}

func deletionTailnet(state publishers.Snapshot, c publishers.Connection) publisherDeletionTailnet {
	t := repositoryTailnet(state, c.Repository.ID)
	shared := false
	for id, other := range state.Tailnets {
		if id != strconv.FormatInt(c.Repository.ID, 10) && t.ClientID != "" && other.ClientID == t.ClientID {
			shared = true
		}
	}
	for _, other := range state.Connections {
		if other.Repository.ID != c.Repository.ID && t.ClientID != "" && repositoryTailnet(state, other.Repository.ID).ClientID == t.ClientID {
			shared = true
		}
	}
	return publisherDeletionTailnet{Tailnet: t, Shared: shared, CredentialsURL: tailscaleCredentialsURL}
}
func (m *publisherIntegration) removalPlan(ctx context.Context, state *publishers.Snapshot, index int) (publisherDeletionPlan, error) {
	c := state.Connections[index]
	for i, other := range state.Connections {
		if i != index && other.Repository.ID == c.Repository.ID && other.State != "disconnected" {
			return publisherDeletionPlan{}, errors.New("Another active publisher owns this repository; its workflow and Tailscale access must not be removed")
		}
	}
	repo, err := m.resolveDeletionRepository(ctx, *state, providerRepository(c.Repository))
	if err != nil {
		return publisherDeletionPlan{}, err
	}
	p, ok := m.provider.(publisherRemovalProvider)
	if !ok {
		return publisherDeletionPlan{}, errors.New("Workflow cleanup is unavailable; update the GitHub integration before deletion")
	}
	plan, err := p.PlanWorkflowRemoval(ctx, appCredentials(state.App), repo)
	if err != nil {
		return publisherDeletionPlan{}, err
	}
	// Repository name and branch are fresh metadata, not identity. Persist them with the
	// deletion review and bind manual confirmation to that exact reviewed state.
	state.Connections[index].Repository = storedRepository(repo)
	c = state.Connections[index]
	view := publisherDeletionPlan{Workflow: plan, WorkflowVerified: plan.Supported && len(plan.Problems) == 0 && len(plan.Files) == 0, Tailscale: deletionTailnet(*state, c)}
	view.TailscaleReview = deletionTailnetApproval(*state, c)
	if c.Deletion != nil {
		view.PRURL = c.Deletion.PRURL
		view.TailscaleConfirmed = view.WorkflowVerified && c.Deletion.TailscaleConfirmed && c.Deletion.TailscaleApproval == deletionTailnetApproval(*state, c)
	}
	return view, nil
}

// Deletion needs fresh authorization, but must not save unrelated permission
// metadata before its own durable progress/record-removal write.
func (m *publisherIntegration) resolveDeletionRepository(ctx context.Context, state publishers.Snapshot, input githubapp.Repository) (githubapp.Repository, error) {
	if state.App.ID == 0 {
		return githubapp.Repository{}, errors.New("Connect the GitHub App before reviewing workflow cleanup")
	}
	permission, err := m.provider.CheckPermissions(ctx, appCredentials(state.App))
	if err != nil || !permission.VariablesWrite {
		return githubapp.Repository{}, errors.New("Cannot verify GitHub App permissions for publisher cleanup")
	}
	repos, err := m.provider.Repositories(ctx, appCredentials(state.App))
	if err != nil {
		return githubapp.Repository{}, errors.New("Cannot verify GitHub repository access")
	}
	for _, repo := range repos {
		// A rename must not strand cleanup. Bind access to the stable repository
		// and installation IDs, then use GitHub's current name for every operation.
		if repo.ID == input.ID && repo.InstallationID == input.InstallationID {
			if err := m.provider.VerifyRepositoryPermissions(ctx, appCredentials(state.App), repo); err != nil {
				return githubapp.Repository{}, err
			}
			return repo, nil
		}
	}
	return githubapp.Repository{}, errors.New("The configured GitHub App cannot access this publisher's saved repository ID through its original installation; restore repository access before retrying workflow cleanup")
}

func deletionTailnetApproval(state publishers.Snapshot, c publishers.Connection) string {
	data, _ := json.Marshal(struct {
		AppID      int64
		Repository publishers.Repository
		Tailnet    publisherDeletionTailnet
	}{state.App.ID, c.Repository, deletionTailnet(state, c)})
	return digestID(string(data))
}

func publisherTailscaleRevocationAttested(c publishers.Connection) bool {
	return c.Deletion != nil && (c.Deletion.TailscaleRevocationAttested || c.Deletion.TailscaleConfirmed)
}

func (s *Server) publisherDeletionReview(w http.ResponseWriter, r *http.Request) {
	s.publisherDeletionAction(w, r, "plan")
}
func (s *Server) publisherDeletionWorkflow(w http.ResponseWriter, r *http.Request) {
	s.publisherDeletionAction(w, r, "workflow")
}
func (s *Server) publisherDeletionVerify(w http.ResponseWriter, r *http.Request) {
	s.publisherDeletionAction(w, r, "verify")
}
func (s *Server) publisherDeletionTailscale(w http.ResponseWriter, r *http.Request) {
	s.publisherDeletionAction(w, r, "tailscale")
}

// Tailscale confirmation is an explicit administrator attestation, not an API
// verification. Services stores no Tailscale administrator credential.
func (s *Server) publisherDeletionAction(w http.ResponseWriter, r *http.Request, action string) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Fingerprint       string `json:"fingerprint"`
		Confirmed         bool   `json:"confirmed"`
		ClientID          string `json:"clientId"`
		Scope             string `json:"scope"`
		ReviewFingerprint string `json:"reviewFingerprint"`
	}
	if !authDecode(w, r, &body) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		http.Error(w, "Publisher storage unavailable", 503)
		return
	}
	index := -1
	for i, c := range state.Connections {
		if c.ID == r.PathValue("id") {
			index = i
			break
		}
	}
	if index < 0 {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	plan, err := m.removalPlan(ctx, &state, index)
	if err != nil {
		http.Error(w, "Cannot review publisher deletion: "+err.Error(), 409)
		return
	}
	c := &state.Connections[index]
	if c.Deletion == nil {
		c.Deletion = &publishers.Deletion{}
	}
	c.Deletion.TailscaleRevocationAttested = publisherTailscaleRevocationAttested(*c)
	c.Deletion.WorkflowVerified = plan.WorkflowVerified
	if !plan.TailscaleConfirmed {
		c.Deletion.TailscaleConfirmed = false
		c.Deletion.TailscaleApproval = ""
	}
	status := 200
	switch action {
	case "workflow":
		if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(body.Fingerprint) || body.Fingerprint != plan.Workflow.Fingerprint || !plan.Workflow.Supported || len(plan.Workflow.Problems) > 0 || len(plan.Workflow.Files) == 0 {
			http.Error(w, "Review a fresh, supported workflow cleanup plan before opening its pull request", 409)
			return
		}
		// Save intent before any uncertain remote write; provider retries verify the
		// same fingerprint-owned branch and PR rather than creating duplicates.
		if err = m.store.Save(state); err != nil {
			http.Error(w, "Cannot persist deletion intent; no remote changes attempted", 503)
			return
		}
		pr, err := m.provider.(publisherRemovalProvider).RemoveWorkflow(ctx, appCredentials(state.App), providerRepository(c.Repository), body.Fingerprint)
		if err != nil {
			http.Error(w, "Cleanup pull request could not be verified. Review GitHub and request a fresh plan before retrying.", 409)
			return
		}
		if !regexp.MustCompile(`^https://github\.com/` + regexp.QuoteMeta(c.Repository.FullName) + `/pull/[1-9][0-9]*$`).MatchString(pr) {
			http.Error(w, "Cleanup pull request URL could not be verified", 502)
			return
		}
		c.Deletion.PRURL = pr
		plan.PRURL = pr
		status = 201
	case "verify":
		if !plan.WorkflowVerified {
			if err = m.store.Save(state); err != nil {
				http.Error(w, "Cannot persist incomplete workflow cleanup", 503)
				return
			}
			http.Error(w, "Workflow cleanup is incomplete. Merge the cleanup PR or remove the integration manually, then verify again.", 409)
			return
		}
	case "tailscale":
		if !plan.WorkflowVerified {
			http.Error(w, "Verify workflow cleanup before confirming Tailscale revocation", 409)
			return
		}
		if !body.Confirmed || body.ReviewFingerprint != plan.TailscaleReview || plan.Tailscale.ClientID == "" || body.ClientID != plan.Tailscale.ClientID || (body.Scope != "dedicated" && body.Scope != "repository") || (plan.Tailscale.Shared && body.Scope != "repository") {
			http.Error(w, "Confirm the current publisher credential or repository-scoped trust removal; shared credentials must not be revoked", 409)
			return
		}
		c.Deletion.TailscaleConfirmed = true
		c.Deletion.TailscaleRevocationAttested = true
		c.Deletion.TailscaleApproval = deletionTailnetApproval(state, *c)
		plan.TailscaleConfirmed = true
	}
	if err = m.store.Save(state); err != nil {
		http.Error(w, "Cannot persist publisher deletion progress; refresh saved state before retrying", 503)
		return
	}
	authJSON(w, status, plan)
}
