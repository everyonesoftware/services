package dashboard

import (
	"database/sql"
	"path/filepath"
	"testing"

	"services/internal/auth"
	"services/internal/publishers"
	"services/internal/updates"
)

func TestPublisherRepositoryReviewFingerprintScope(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	connectionForTest(t, s, store, cookie)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	state.Connections[0].Reconciliation = &publishers.Reconciliation{State: "review_required", PreviousName: "acme/app", CurrentName: "acme/renamed", CheckedAt: "before"}
	tests := []struct {
		name       string
		change     func(*publishers.Snapshot, *publishers.Connection, *updates.PublicationBinding, *publishers.Repository)
		invalidate bool
	}{
		{"app_id", func(s *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			s.App.ID++
		}, true},
		{"app_slug", func(s *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			s.App.Slug = "other"
		}, true},
		{"app_pem", func(s *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			s.App.PEM = "other-private-key"
		}, true},
		{"connection_id", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.ID = "other"
		}, true},
		{"state", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.State = "error"
		}, true},
		{"deletion", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.Deletion = &publishers.Deletion{}
		}, true},
		{"remote_disable", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.RemoteDisablePending = true
		}, true},
		{"configured", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.Configured = false
		}, true},
		{"owner", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.RepositoryOwner = "other"
		}, true},
		{"image", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.ImageRepository = "ghcr.io/acme/other"
		}, true},
		{"current_key", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.KeyID = "rotated"
		}, true},
		{"current_secret", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.Secret = "rotated"
		}, true},
		{"previous_key", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.PreviousKeyID = "rotated"
		}, true},
		{"previous_secret", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.PreviousSecret = "rotated"
		}, true},
		{"tailnet_client", func(s *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			v := s.Tailnets["12"]
			v.ClientID = "other"
			s.Tailnets["12"] = v
		}, true},
		{"tailnet_audience", func(s *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			v := s.Tailnets["12"]
			v.Audience = "other"
			s.Tailnets["12"] = v
		}, true},
		{"tailnet_tags", func(s *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			v := s.Tailnets["12"]
			v.Tags = "tag:other"
			s.Tailnets["12"] = v
		}, true},
		{"binding_source", func(_ *publishers.Snapshot, _ *publishers.Connection, b *updates.PublicationBinding, _ *publishers.Repository) {
			b.SourceRepository = "acme/other"
		}, true},
		{"binding_revision", func(_ *publishers.Snapshot, _ *publishers.Connection, b *updates.PublicationBinding, _ *publishers.Repository) {
			b.Revision++
		}, true},
		{"binding_repository_id", func(_ *publishers.Snapshot, _ *publishers.Connection, b *updates.PublicationBinding, _ *publishers.Repository) {
			b.RepositoryID++
		}, true},
		{"binding_installation_id", func(_ *publishers.Snapshot, _ *publishers.Connection, b *updates.PublicationBinding, _ *publishers.Repository) {
			b.InstallationID++
		}, true},
		{"authorized_name", func(_ *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, r *publishers.Repository) {
			r.FullName = "acme/other"
		}, true},
		{"authorized_id", func(_ *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, r *publishers.Repository) {
			r.ID++
		}, true},
		{"authorized_installation", func(_ *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, r *publishers.Repository) {
			r.InstallationID++
		}, true},
		{"authorized_branch", func(_ *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, r *publishers.Repository) {
			r.DefaultBranch = "next"
		}, true},
		{"eligibility_generation", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.PublicationEligibleAfter = "changed-generation"
		}, true},
		{"receipt_evidence", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.LastPublication = &publishers.PublicationReceipt{Digest: "old-inflight-receipt"}
			c.LastPublicationReconciliation = "old-generation"
			c.LastPublicationRunID = "123"
			c.LastPublicationRunAttempt = 2
		}, false},
		{"checked_at", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.Reconciliation.CheckedAt = "after"
		}, false},
		{"journal", func(_ *publishers.Snapshot, c *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			c.ReconciliationReview = &publishers.ReconciliationReview{Fingerprint: "journal"}
		}, false},
		{"manifest_handshake", func(s *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			s.Pending = publishers.Pending{Hash: "unrelated-handshake", Expires: 123}
		}, false},
		{"app_hints", func(s *publishers.Snapshot, _ *publishers.Connection, _ *updates.PublicationBinding, _ *publishers.Repository) {
			s.App.PermissionsChecked = !s.App.PermissionsChecked
			s.App.VariablesWrite = !s.App.VariablesWrite
			s.App.SettingsURL = "other"
		}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			copy := state
			copy.Tailnets = map[string]publishers.Tailnet{"12": state.Tailnets["12"]}
			c := state.Connections[0]
			reconciliation := *c.Reconciliation
			c.Reconciliation = &reconciliation
			binding := updates.PublicationBinding{ImageRepository: c.ImageRepository, SourceRepository: "acme/app", Revision: 1}
			repo := c.Repository
			repo.FullName = "acme/renamed"
			before, err := s.repositoryReviewFingerprint(copy, c, binding, repo)
			if err != nil {
				t.Fatal(err)
			}
			test.change(&copy, &c, &binding, &repo)
			after, err := s.repositoryReviewFingerprint(copy, c, binding, repo)
			if err != nil || (before != after) != test.invalidate {
				t.Fatalf("fingerprint invalidation=%v, want %v (err=%v)", before != after, test.invalidate, err)
			}
		})
	}
	// A saved legacy connection inherits the global value only without a
	// repository-specific override. Use exactly the production selector.
	state.Tailnets = nil
	c := state.Connections[0]
	binding := updates.PublicationBinding{ImageRepository: c.ImageRepository}
	before, err := s.repositoryReviewFingerprint(state, c, binding, c.Repository)
	if err != nil {
		t.Fatal(err)
	}
	state.Tailnet.Audience = "changed-effective-global"
	after, err := s.repositoryReviewFingerprint(state, c, binding, c.Repository)
	if err != nil || before == after {
		t.Fatalf("effective legacy trust did not invalidate: %v", err)
	}
}

func TestPublisherRepositoryReviewFingerprintCredentialMetadata(t *testing.T) {
	for _, role := range []string{"current", "previous"} {
		for _, mutation := range []string{"revoke", "scope", "repository", "expiry", "last_used"} {
			t.Run(role+"/"+mutation, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "auth.db")
				keys, err := auth.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer keys.Close()
				current, secret, err := keys.CreateKey("current", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
				if err != nil {
					t.Fatal(err)
				}
				previous, previousSecret, err := keys.CreateKey("previous", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
				if err != nil {
					t.Fatal(err)
				}
				c := publishers.Connection{ID: "affected", KeyID: current.ID, Secret: secret, PreviousKeyID: previous.ID, PreviousSecret: previousSecret, ImageRepository: "ghcr.io/acme/app"}
				s := &Server{authStore: keys, publishers: &publisherIntegration{endpoint: "https://localhost/api/images/published"}}
				before, err := s.repositoryReviewFingerprint(publishers.Snapshot{}, c, updates.PublicationBinding{}, c.Repository)
				if err != nil {
					t.Fatal(err)
				}
				target := current.ID
				if role == "previous" {
					target = previous.ID
				}
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				query := map[string]string{
					"revoke":     "DELETE FROM api_keys WHERE id=?",
					"scope":      `UPDATE api_keys SET scopes='["services:read"]' WHERE id=?`,
					"repository": `UPDATE api_keys SET repositories='["ghcr.io/acme/other"]' WHERE id=?`,
					"expiry":     "UPDATE api_keys SET expires_at=1 WHERE id=?",
					"last_used":  "UPDATE api_keys SET last_used_at=1 WHERE id=?",
				}[mutation]
				if _, err := db.Exec(query, target); err != nil {
					t.Fatal(err)
				}
				after, err := s.repositoryReviewFingerprint(publishers.Snapshot{}, c, updates.PublicationBinding{}, c.Repository)
				if err != nil || (before != after) != (mutation != "last_used") {
					t.Fatalf("%s %s credential invalidation=%v: %v", role, mutation, before != after, err)
				}
			})
		}
	}
}
