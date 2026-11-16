package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"services/internal/auth"
	"services/internal/githubapp"
	"services/internal/groups"
	"services/internal/publishers"
)

type publisherRecoveryProvider interface {
	RecoverConfiguration(context.Context, githubapp.App, githubapp.Repository, string, map[string]string) error
}
type publisherRotationProvider interface {
	ReplaceSecret(context.Context, githubapp.App, githubapp.Repository, string) error
}

// Lifecycle actions are administrator-only. The integration lock serializes them
// with callbacks; the saved credential is never sent to the browser.
func (s *Server) publisherAction(w http.ResponseWriter, r *http.Request, action string) {
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
	if action != "disconnect" && publisherTailscaleRevocationAttested(*c) {
		http.Error(w, "Tailscale revocation was confirmed; complete publisher deletion instead of restoring notification configuration", 409)
		return
	}
	if c.State == "disconnected" && action != "disconnect" {
		http.Error(w, "This publisher is disconnected", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	switch action {
	case "disconnect":
		// Revoke first, including a replacement in progress. Remote availability must
		// never prevent the operator from disabling this local credential.
		for _, id := range []string{c.KeyID, c.PreviousKeyID} {
			if err = s.revokePublisherKey(id); err != nil {
				http.Error(w, "Cannot revoke publisher credential; no success was recorded", 503)
				return
			}
		}
		c.RemoteDisablePending = c.RemoteDisablePending || c.Configured
		c.State = "disconnected"
		c.Secret = ""
		c.PreviousKeyID = ""
		c.PreviousSecret = ""
		c.ChallengeHash = ""
		c.ChallengeUsed = true
		c.Configured = false
		c.VerifiedAt = ""
		c.Error = ""
		// The repository variable is shared across this repository's historical
		// connections. An obsolete cleanup must not disable its live successor.
		if c.RemoteDisablePending {
			for i, other := range state.Connections {
				if i != index && other.Repository.ID == c.Repository.ID && other.State != "disconnected" {
					c.RemoteDisablePending = false
					c.Error = "Local credential revoked. A newer connection owns this repository's notification settings; no remote settings were changed."
					break
				}
			}
		}
		if err = m.store.Save(state); err != nil {
			http.Error(w, "Credential revoked but disconnect could not be persisted; retry", 503)
			return
		}
		// No secrets, workflow files, or shared Tailscale identities are deleted.
		if c.RemoteDisablePending {
			if err = m.provider.SetEnabled(ctx, appCredentials(state.App), providerRepository(c.Repository), false); err != nil {
				c.Error = "Local credential revoked. GitHub notification disable could not be verified; review the repository variable."
			} else {
				c.RemoteDisablePending = false
			}
			if err = m.store.Save(state); err != nil {
				http.Error(w, "Disconnected locally but remote cleanup status could not be saved", 503)
				return
			}
		}
	case "recover":
		if c.Secret == "" {
			http.Error(w, "This setup has no recoverable credential. Disconnect it and resolve the reported repository conflict before reconnecting.", 409)
			return
		}
		if _, err = s.authStore.AuthenticateKey(c.Secret); err != nil {
			http.Error(w, "Saved key was revoked or expired; rotate the key", 409)
			return
		}
		if !c.Configured {
			provider, ok := m.provider.(publisherRecoveryProvider)
			if !ok {
				http.Error(w, "Configuration recovery is unavailable", 503)
				return
			}
			if !validTailnet(repositoryTailnet(state, c.Repository.ID)) {
				http.Error(w, "Register Tailscale federation for this repository before recovery", 409)
				return
			}
			if err = provider.RecoverConfiguration(ctx, appCredentials(state.App), providerRepository(c.Repository), c.Secret, m.variables(state, providerRepository(c.Repository))); err != nil {
				http.Error(w, "Recovery could not reconcile existing configuration. No existing secret was overwritten.", 409)
				return
			}
			c.Configured = true
			if err = m.store.Save(state); err != nil {
				http.Error(w, "Recovery completion could not be persisted", 503)
				return
			}
		}
		if err = m.dispatch(ctx, &state, index); err != nil {
			http.Error(w, "Recovery saved, but push test dispatch was not verified; retry the saved connection", 502)
			return
		}
	case "rotate":
		if !c.Configured {
			http.Error(w, "Complete or recover configuration before rotating its key", 409)
			return
		}
		provider, ok := m.provider.(publisherRotationProvider)
		if !ok {
			http.Error(w, "Key rotation is unavailable", 503)
			return
		}
		// Reuse a valid in-flight replacement after an uncertain remote write.
		// Revoked/expired replacements need a fresh key, preserving the original.
		createReplacement := c.PreviousKeyID == ""
		if !createReplacement {
			_, keyErr := s.authStore.AuthenticateKey(c.Secret)
			if keyErr != nil && !errors.Is(keyErr, auth.ErrUnauthorized) {
				http.Error(w, "Cannot verify replacement credential status", 503)
				return
			}
			createReplacement = errors.Is(keyErr, auth.ErrUnauthorized)
		}
		if createReplacement {
			key, secret, keyErr := s.authStore.CreateKey("GitHub publisher "+c.ID[:12], []string{"publications:write"}, []string{c.ImageRepository}, "")
			if keyErr != nil {
				http.Error(w, "Cannot create replacement credential", 503)
				return
			}
			if c.PreviousKeyID == "" {
				c.PreviousKeyID = c.KeyID
				c.PreviousSecret = c.Secret
			} else if err = s.revokePublisherKey(c.KeyID); err != nil {
				_ = s.revokePublisherKey(key.ID)
				http.Error(w, "Cannot retire invalid replacement credential", 503)
				return
			}
			c.KeyID = key.ID
			c.Secret = secret
			c.State = "error"
			c.Error = "Key rotation is pending push verification."
			c.VerifiedAt = ""
			// Invalidate prior proofs before any change to the GitHub secret.
			c.ChallengeHash = ""
			c.ChallengeUsed = true
			if err = m.store.Save(state); err != nil {
				_ = s.revokePublisherKey(key.ID)
				http.Error(w, "Cannot persist replacement credential", 503)
				return
			}
		}
		if err = provider.ReplaceSecret(ctx, appCredentials(state.App), providerRepository(c.Repository), c.Secret); err != nil {
			http.Error(w, "Replacement secret delivery was not verified. Previous key remains active; retry rotation to reuse the saved replacement.", 502)
			return
		}
		if err = m.dispatch(ctx, &state, index); err != nil {
			http.Error(w, "Replacement saved, but push test dispatch was not verified; retry the saved connection", 502)
			return
		}
	}
	authJSON(w, 200, map[string]any{"connection": connectionView(state.Connections[index])})
}
func (s *Server) publisherRecover(w http.ResponseWriter, r *http.Request) {
	s.publisherAction(w, r, "recover")
}
func (s *Server) publisherRotate(w http.ResponseWriter, r *http.Request) {
	s.publisherAction(w, r, "rotate")
}
func (s *Server) publisherDisconnect(w http.ResponseWriter, r *http.Request) {
	s.publisherAction(w, r, "disconnect")
}
func (s *Server) revokePublisherKey(id string) error {
	if id == "" {
		return nil
	}
	err := s.authStore.RevokeKey(id)
	if errors.Is(err, auth.ErrNotFound) {
		return nil
	}
	return err
}

// Managed connections bind image provenance as well as the existing key image
// allowlist. The check is local: it never contacts a registry or schedules work.
func (s *Server) publisherProvenance(key auth.APIKey, p groups.ImagePublication) error {
	if s.publishers == nil {
		if s.publisherConfigurationAttempted {
			return errors.New("publisher configuration unavailable")
		}
		return nil
	}
	m := s.publishers
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		return err
	}
	for _, c := range state.Connections {
		if c.KeyID != key.ID && c.PreviousKeyID != key.ID {
			continue
		}
		if c.State == "disconnected" || c.ImageRepository != p.Repository || c.Repository.FullName != p.SourceRepository {
			return auth.ErrUnauthorized
		}
	}
	return nil
}
func (s *Server) recordPublisherReceipt(key auth.APIKey, p groups.ImagePublication, result string) error {
	if s.publishers == nil || result == "stale" {
		return nil
	}
	m := s.publishers
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		return err
	}
	for i := range state.Connections {
		c := &state.Connections[i]
		if c.KeyID != key.ID && c.PreviousKeyID != key.ID {
			continue
		}
		if c.State == "disconnected" || c.ImageRepository != p.Repository || c.Repository.FullName != p.SourceRepository {
			return auth.ErrUnauthorized
		}
		if !publisherReceiptIsNewer(p, *c) {
			return nil
		}
		c.LastPublication = &publishers.PublicationReceipt{Version: p.Version, Digest: p.Digest, Tags: append([]string(nil), p.Tags...), Commit: p.Commit, RunURL: p.RunURL, PublishedAt: p.PublishedAt, ReceivedAt: time.Now().UTC().Format(time.RFC3339), Result: result}
		c.LastPublicationRunID = p.RunID
		c.LastPublicationRunAttempt = p.RunAttempt
		return m.store.Save(state)
	}
	return nil
}

// Match updates publication freshness: publish time, then numeric run ID, then
// attempt. Called under the integration lock after ingestion, so a slow older
// Accept cannot regress a newer receipt. A duplicate may repair a failed save.
func publisherReceiptIsNewer(p groups.ImagePublication, c publishers.Connection) bool {
	old := c.LastPublication
	if old == nil {
		return true
	}
	published, _ := time.Parse(time.RFC3339Nano, p.PublishedAt)
	previous, _ := time.Parse(time.RFC3339Nano, old.PublishedAt)
	if !published.Equal(previous) {
		return published.After(previous)
	}
	run, attempt := c.LastPublicationRunID, c.LastPublicationRunAttempt
	if run == "" {
		// Old receipts predate private ordering metadata. Recover run identity
		// from its validated URL. The attempt cannot be recovered; conservatively
		// preserve tied events from that run rather than risk regressing it.
		run = old.RunURL[strings.LastIndex(old.RunURL, "/")+1:]
		if run == p.RunID {
			return false
		}
	}
	if len(p.RunID) != len(run) {
		return len(p.RunID) > len(run)
	}
	if p.RunID != run {
		return p.RunID > run
	}
	return p.RunAttempt > attempt
}
