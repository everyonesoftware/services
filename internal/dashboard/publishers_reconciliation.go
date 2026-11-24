package dashboard

import (
	"context"
	"errors"
	"strings"
	"time"

	"services/internal/githubapp"
	"services/internal/publishers"
)

var errPublisherIdentity = errors.New("publisher_identity_mismatch")
var errPublisherUnavailable = errors.New("publisher_unavailable")

func repositoryOwner(name string) string {
	owner, _, _ := strings.Cut(name, "/")
	return strings.ToLower(owner)
}

func authorizedPublisherRepository(ctx context.Context, m *publisherIntegration, state publishers.Snapshot, c publishers.Connection, repositories []githubapp.Repository) (githubapp.Repository, error) {
	owner := c.RepositoryOwner
	if owner == "" {
		owner = repositoryOwner(c.Repository.FullName)
	}
	for _, repo := range repositories {
		if repo.ID != c.Repository.ID || repo.InstallationID != c.Repository.InstallationID {
			continue
		}
		if repo.ID <= 0 || repo.InstallationID <= 0 || repositoryOwner(repo.FullName) != owner || !strings.Contains(repo.FullName, "/") {
			return githubapp.Repository{}, errPublisherIdentity
		}
		if err := m.provider.VerifyRepositoryPermissions(ctx, appCredentials(state.App), repo); err != nil {
			return githubapp.Repository{}, errPublisherUnavailable
		}
		return repo, nil
	}
	return githubapp.Repository{}, errPublisherIdentity
}

func publisherReconciliation(c publishers.Connection, current, status string) *publishers.Reconciliation {
	previous := c.Repository.FullName
	if c.Reconciliation != nil && c.Reconciliation.PreviousName != "" {
		previous = c.Reconciliation.PreviousName
	}
	message := "Repository history requires explicit administrator review."
	switch status {
	case "awaiting_publication":
		message = "Repository identity reconciled. Waiting for an eligible new image publication."
	case "reconciled":
		message = "A new image publication was accepted after repository reconciliation."
	case "identity_mismatch":
		message = "The original repository identity or owner is no longer authorized."
	case "unavailable":
		message = "Repository authorization could not be verified. Retry when GitHub is available."
	}
	return &publishers.Reconciliation{State: status, PreviousName: previous, CurrentName: current, RepositoryID: c.Repository.ID, InstallationID: c.Repository.InstallationID, Message: message, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}
}

// One total timeout bounds discovery including per-repository permission checks.
// Name refresh never migrates historical image ownership.
func (s *Server) discoverPublisherRepositories(parent context.Context, state *publishers.Snapshot) bool {
	if len(state.Connections) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	m := s.publishers
	permission, err := m.provider.CheckPermissions(ctx, appCredentials(state.App))
	var repos []githubapp.Repository
	if err == nil && permission.VariablesWrite && state.App.ID > 0 {
		repos, err = m.provider.Repositories(ctx, appCredentials(state.App))
	} else {
		err = errPublisherUnavailable
	}
	changed := false
	for i := range state.Connections {
		c := &state.Connections[i]
		if c.State == "disconnected" || c.Deletion != nil || c.ReconciliationReview != nil {
			continue
		}
		if c.RepositoryOwner == "" {
			c.RepositoryOwner = repositoryOwner(c.Repository.FullName)
			changed = true
		}
		if err != nil {
			c.Reconciliation = publisherReconciliation(*c, c.Repository.FullName, "unavailable")
			changed = true
			continue
		}
		repo, resolveErr := authorizedPublisherRepository(ctx, m, *state, *c, repos)
		if resolveErr != nil {
			status := "unavailable"
			if errors.Is(resolveErr, errPublisherIdentity) {
				status = "identity_mismatch"
			}
			c.Reconciliation = publisherReconciliation(*c, c.Repository.FullName, status)
			changed = true
			continue
		}
		previousName := c.Repository.FullName
		if repo.FullName != c.Repository.FullName {
			c.Reconciliation = publisherReconciliation(*c, repo.FullName, "review_required")
			c.Repository = storedRepository(repo)
			c.PublicationEligibleAfter = time.Now().UTC().Format(time.RFC3339Nano)
			changed = true
		}
		if bindingProvider, ok := s.publicationReceiver.(publicationBindingProvider); ok {
			binding, bindingErr := bindingProvider.PublicationRepositoryBinding(ctx, c.ImageRepository)
			if bindingErr != nil {
				c.Reconciliation = publisherReconciliation(*c, repo.FullName, "unavailable")
				changed = true
				continue
			}
			if binding.SourceRepository != "" && (repositoryOwner(binding.SourceRepository) != repositoryOwner(repo.FullName) || (binding.RepositoryID != 0 && (binding.RepositoryID != repo.ID || binding.InstallationID != repo.InstallationID))) {
				c.Reconciliation = publisherReconciliation(*c, repo.FullName, "identity_mismatch")
				changed = true
				continue
			}
			if binding.SourceRepository != "" && binding.RepositoryID == 0 {
				c.Reconciliation = publisherReconciliation(*c, repo.FullName, "review_required")
				c.Reconciliation.PreviousName = binding.SourceRepository
				changed = true
				continue
			}
			if c.Reconciliation != nil && (c.Reconciliation.State == "unavailable" || c.Reconciliation.State == "awaiting_publication") &&
				previousName == repo.FullName && binding.SourceRepository == repo.FullName && binding.ImageRepository == c.ImageRepository &&
				binding.RepositoryID == repo.ID && binding.InstallationID == repo.InstallationID && binding.Revision > 0 && publisherHasCurrentReconciliationReceipt(*c) {
				c.Reconciliation = publisherReconciliation(*c, repo.FullName, "reconciled")
				changed = true
				continue
			}
			recovering := c.Reconciliation != nil && c.Reconciliation.State != "awaiting_publication" && c.Reconciliation.State != "reconciled"
			if (binding.SourceRepository == "" && binding.RepositoryID == 0) || (binding.RepositoryID == repo.ID && binding.InstallationID == repo.InstallationID && (binding.SourceRepository != repo.FullName || previousName != repo.FullName || recovering)) {
				if bindingErr = reconcileAndReadPublicationBinding(ctx, bindingProvider, binding, storedRepository(repo)); bindingErr != nil {
					c.Reconciliation = publisherReconciliation(*c, repo.FullName, "unavailable")
				} else {
					c.Reconciliation = publisherReconciliation(*c, repo.FullName, "awaiting_publication")
					if binding.SourceRepository != "" && binding.SourceRepository != repo.FullName {
						c.Reconciliation.PreviousName = binding.SourceRepository
					}
					c.Reconciliation.Message = "Repository name reconciled using its verified identity. Waiting for a new image publication."
					if c.PublicationEligibleAfter == "" || previousName != repo.FullName || binding.SourceRepository != repo.FullName {
						c.PublicationEligibleAfter = time.Now().UTC().Format(time.RFC3339Nano)
					}
				}
				changed = true
			}
		}
	}
	return changed
}
