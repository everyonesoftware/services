package dashboard

import (
	"context"
	"errors"
	"net/http"
	"time"

	"services/internal/auth"
	"services/internal/githubapp"
)

type publisherReconnectProvider interface {
	ReconnectConfiguration(context.Context, githubapp.App, githubapp.Repository, string, map[string]string) error
}

func (s *Server) publisherReconnect(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var body struct{}
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
	c := &state.Connections[index]
	if c.State != "disconnected" {
		http.Error(w, "Only disconnected publishers can be reconnected", 409)
		return
	}
	if publisherTailscaleRevocationAttested(*c) {
		http.Error(w, "Tailscale revocation was confirmed for this publisher. Complete deletion and add a new publisher to reconnect with fresh access.", 409)
		return
	}
	for i, other := range state.Connections {
		if i != index && other.Repository.ID == c.Repository.ID && other.State != "disconnected" {
			http.Error(w, "Another saved connection owns this repository. Disconnect its active successor before reconnecting this publisher.", 409)
			return
		}
	}
	provider, ok := m.provider.(publisherReconnectProvider)
	if !ok {
		http.Error(w, "Explicit configuration reconnect is unavailable", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	repo, err := m.resolve(ctx, &state, publisherSetupRequest{Repository: providerRepository(c.Repository), ImageRepository: c.ImageRepository})
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	inspection, err := m.provider.Inspect(ctx, appCredentials(state.App), repo)
	if err != nil {
		http.Error(w, "Cannot inspect GitHub workflow", 502)
		return
	}
	if !inspection.Supported {
		http.Error(w, "Review and merge the publishing workflow before reconnecting", 409)
		return
	}
	// A disconnected record can retain only a fresh, in-flight reconnect key.
	// Reuse that durable key after an uncertain remote write, never a revoked one.
	createKey := c.Secret == ""
	if !createKey {
		_, keyErr := s.authStore.AuthenticateKey(c.Secret)
		if keyErr != nil && !errors.Is(keyErr, auth.ErrUnauthorized) {
			http.Error(w, "Cannot verify saved reconnect credential", 503)
			return
		}
		createKey = errors.Is(keyErr, auth.ErrUnauthorized)
	}
	if createKey {
		key, secret, err := s.authStore.CreateKey("GitHub publisher "+c.ID, []string{"publications:write"}, []string{c.ImageRepository}, "")
		if err != nil {
			http.Error(w, "Cannot create fresh publisher credential", 503)
			return
		}
		c.KeyID = key.ID
		c.Secret = secret
		c.PreviousKeyID = ""
		c.PreviousSecret = ""
		c.Configured = false
		c.VerifiedAt = ""
		c.ChallengeHash = ""
		c.ChallengeUsed = true
		c.RemoteDisablePending = true
		c.Error = "Reconnect pending. Retry reconnect to reconcile the saved fresh credential."
		if err = m.store.Save(state); err != nil {
			_ = s.revokePublisherKey(key.ID)
			http.Error(w, "Cannot persist reconnect credential; no remote changes attempted", 503)
			return
		}
	}
	if err = provider.ReconnectConfiguration(ctx, appCredentials(state.App), repo, c.Secret, m.variables(state, repo)); err != nil {
		http.Error(w, "Reconnect configuration was not verified. Review GitHub settings and retry reconnect with the saved credential; conflicting variables are never overwritten.", 502)
		return
	}
	c.State = "configured"
	c.Error = ""
	c.Configured = true
	c.RemoteDisablePending = false
	if err = m.store.Save(state); err != nil {
		http.Error(w, "Reconnect configuration may have changed but completion could not be saved; retry reconnect", 503)
		return
	}
	if err = m.dispatch(ctx, &state, index); err != nil {
		http.Error(w, "Reconnect saved, but test dispatch was not verified; retry the connection test", 502)
		return
	}
	authJSON(w, 200, map[string]any{"connection": connectionView(state.Connections[index])})
}

// Removal forgets the local disconnected record only after fresh workflow
// verification and an administrator's scoped Tailscale revocation attestation.
func (s *Server) publisherRemove(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		http.Error(w, "Publisher storage unavailable", 503)
		return
	}
	for i, c := range state.Connections {
		if c.ID != r.PathValue("id") {
			continue
		}
		if c.State != "disconnected" {
			http.Error(w, "Disconnect this publisher before removing it", 409)
			return
		}
		if c.RemoteDisablePending {
			http.Error(w, "GitHub cleanup is pending. Retry disconnect to verify notifications are disabled before removing this publisher.", 409)
			return
		}
		if c.Deletion == nil || !c.Deletion.WorkflowVerified || !c.Deletion.TailscaleConfirmed || c.Deletion.TailscaleApproval != deletionTailnetApproval(state, c) {
			http.Error(w, "Complete workflow cleanup and confirm current Tailscale access revocation before deleting this publisher", 409)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		plan, err := m.removalPlan(ctx, &state, i)
		if err != nil || !plan.WorkflowVerified || !plan.TailscaleConfirmed {
			http.Error(w, "Cannot verify current workflow cleanup or Tailscale confirmation. Refresh the deletion review before retrying.", 409)
			return
		}
		state.Connections = append(state.Connections[:i], state.Connections[i+1:]...)
		if err = m.store.Save(state); err != nil {
			http.Error(w, "Cannot persist publisher removal; retry", 503)
			return
		}
		authJSON(w, 200, map[string]bool{"removed": true})
		return
	}
	http.NotFound(w, r)
}
