package dashboard

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"services/internal/auth"
	"services/internal/githubapp"
	"services/internal/publishers"
)

type publisherRepairProvider interface {
	PlanConfiguration(context.Context, githubapp.App, githubapp.Repository, map[string]string) (githubapp.ConfigurationPlan, error)
	ApplyConfigurationRepair(context.Context, githubapp.App, githubapp.Repository, string, map[string]string, string) error
}

type publisherRepairPlan struct {
	Repository      githubapp.Repository        `json:"repository"`
	ImageRepository string                      `json:"imageRepository"`
	Configuration   githubapp.ConfigurationPlan `json:"configuration"`
}

// Bind GitHub's reviewed snapshot to the exact local owner and desired trust.
// No credential plaintext is returned or included in the browser's review.
func repairApproval(state publishers.Snapshot, c publishers.Connection, plan publisherRepairPlan, variables map[string]string) string {
	data, _ := json.Marshal(struct {
		AppID           int64
		AppSlug         string
		ConnectionID    string
		SavedRepository publishers.Repository
		KeyID           string
		PreviousKeyID   string
		Configured      bool
		State           string
		Tailnet         publishers.Tailnet
		Variables       map[string]string
		Plan            publisherRepairPlan
	}{state.App.ID, state.App.Slug, c.ID, c.Repository, c.KeyID, c.PreviousKeyID, c.Configured, c.State, repositoryTailnet(state, c.Repository.ID), variables, plan})
	return digestID(string(data))
}

func (s *Server) publisherRepairReview(w http.ResponseWriter, r *http.Request) {
	s.publisherRepairAction(w, r, false)
}
func (s *Server) publisherRepair(w http.ResponseWriter, r *http.Request) {
	s.publisherRepairAction(w, r, true)
}
func (s *Server) publisherRepairAction(w http.ResponseWriter, r *http.Request, apply bool) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if apply {
		if !authDecode(w, r, &body) {
			return
		}
	} else {
		var empty struct{}
		if !authDecode(w, r, &empty) {
			return
		}
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
	c := &state.Connections[index]
	if publisherTailscaleRevocationAttested(*c) {
		http.Error(w, "Tailscale revocation was confirmed; complete publisher deletion instead of repairing access", 409)
		return
	}
	if c.State == "verified" || c.State == "awaiting_callback" || (c.Configured && c.State != "error" && c.State != "disconnected") {
		http.Error(w, "A verified publisher or pending push test cannot be repaired; inspect the current test first", 409)
		return
	}
	for i, other := range state.Connections {
		if i != index && other.Repository.ID == c.Repository.ID && other.State != "disconnected" {
			http.Error(w, "Another active successor owns this repository; repair that connection instead", 409)
			return
		}
	}
	provider, ok := m.provider.(publisherRepairProvider)
	if !ok {
		http.Error(w, "Reviewed configuration repair is unavailable", 503)
		return
	}
	if !validTailnet(repositoryTailnet(state, c.Repository.ID)) {
		http.Error(w, "Register valid Tailscale federation for this repository before repair", 409)
		return
	}
	if len(c.ImageRepository) > 255 || !publicationRepository.MatchString(c.ImageRepository) {
		http.Error(w, "Saved publisher image repository is invalid", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	repo, err := m.resolveDeletionRepository(ctx, state, providerRepository(c.Repository))
	if err != nil {
		http.Error(w, "Cannot authorize publisher repair: "+err.Error(), 403)
		return
	}
	inspection, err := m.provider.Inspect(ctx, appCredentials(state.App), repo)
	if err != nil {
		http.Error(w, "Cannot inspect GitHub workflow for repair; retry review", 502)
		return
	}
	if !inspection.Supported {
		http.Error(w, "Review and merge the supported publishing workflow before repair", 409)
		return
	}
	variables := m.variables(state, repo)
	configuration, err := provider.PlanConfiguration(ctx, appCredentials(state.App), repo, variables)
	if err != nil {
		http.Error(w, "Cannot review current GitHub configuration; check App access and retry review", 502)
		return
	}
	plan := publisherRepairPlan{Repository: repo, ImageRepository: c.ImageRepository, Configuration: configuration}
	fingerprint := repairApproval(state, *c, plan, variables)
	if !apply {
		plan.Configuration.Fingerprint = fingerprint
		authJSON(w, 200, plan)
		return
	}
	if len(body.Fingerprint) != 64 || subtle.ConstantTimeCompare([]byte(body.Fingerprint), []byte(fingerprint)) != 1 {
		http.Error(w, "Publisher configuration or local connection changed. Review a fresh repair plan before applying.", 409)
		return
	}
	// Validate saved credentials without ever restoring an expired/revoked key.
	create := c.Secret == ""
	if !create {
		key, keyErr := s.authStore.AuthenticateKey(c.Secret)
		if keyErr != nil && !errors.Is(keyErr, auth.ErrUnauthorized) {
			http.Error(w, "Cannot verify saved publisher credential; no remote changes attempted", 503)
			return
		}
		create = errors.Is(keyErr, auth.ErrUnauthorized)
		if keyErr == nil && !publisherCredentialMatches(key, *c) {
			http.Error(w, "Saved publisher credential identity or scope does not match; revoke it from Accounts before repair", 409)
			return
		}
	}
	newKey := ""
	if create {
		// A missing plaintext can leave a live key identity behind. Retire that
		// unusable credential rather than orphaning it when replacing the ID.
		if err = s.revokePublisherKey(c.KeyID); err != nil {
			http.Error(w, "Cannot retire unavailable publisher credential; no remote changes attempted", 503)
			return
		}
		key, secret, keyErr := s.authStore.CreateKey("GitHub publisher "+c.ID, []string{"publications:write"}, []string{c.ImageRepository}, "")
		if keyErr != nil {
			http.Error(w, "Cannot create scoped publisher repair credential", 503)
			return
		}
		c.KeyID = key.ID
		c.Secret = secret
		newKey = key.ID
	}
	// Callback proof and configuration must be invalid before any remote write.
	// Keep any previous rotation credential for callback-time revocation only.
	c.Repository = storedRepository(repo)
	c.Configured = false
	c.State = "error"
	c.VerifiedAt = ""
	c.ChallengeHash = ""
	c.ChallengeExpires = 0
	c.ChallengeUsed = true
	c.RunID = ""
	c.RemoteDisablePending = true
	c.Error = "Reviewed repair is pending. Review a fresh repair plan and retry to reuse the saved credential."
	if err = m.store.Save(state); err != nil {
		if newKey != "" {
			_ = s.revokePublisherKey(newKey)
		}
		http.Error(w, "Cannot persist repair intent; no remote changes attempted", 503)
		return
	}
	if err = provider.ApplyConfigurationRepair(ctx, appCredentials(state.App), repo, c.Secret, variables, configuration.Fingerprint); err != nil {
		c.Error = "GitHub repair was not verified. Review a fresh repair plan and retry with the saved credential; check GitHub App permissions and repository settings."
		if saveErr := m.store.Save(state); saveErr != nil {
			http.Error(w, "Repair outcome is uncertain and error status could not be saved; refresh the saved connection before reviewing repair", 503)
			return
		}
		http.Error(w, c.Error, 502)
		return
	}
	c.Configured = true
	c.State = "configured"
	c.Error = ""
	c.RemoteDisablePending = false
	c.Deletion = nil
	if err = m.store.Save(state); err != nil {
		http.Error(w, "GitHub repair was verified but completion could not be saved; review repair again to reuse the saved credential", 503)
		return
	}
	if err = m.dispatch(ctx, &state, index); err != nil {
		http.Error(w, "Repair saved, but test dispatch was not verified; inspect GitHub Actions and retry the connection test", 502)
		return
	}
	authJSON(w, 200, map[string]any{"connection": s.connectionView(state.Connections[index])})
}
