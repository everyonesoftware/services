package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"services/internal/auth"
	"services/internal/publishers"
	"services/internal/updates"
)

// Optional: legacy and fake receivers need not expose historical bindings.
type publicationBindingProvider interface {
	PublicationRepositoryBinding(context.Context, string) (updates.PublicationBinding, error)
	ReconcilePublicationRepository(context.Context, updates.PublicationBinding, int64, int64, string) error
}

func reconcileAndReadPublicationBinding(ctx context.Context, provider publicationBindingProvider, expected updates.PublicationBinding, repo publishers.Repository) error {
	if err := provider.ReconcilePublicationRepository(ctx, expected, repo.ID, repo.InstallationID, repo.FullName); err != nil {
		return err
	}
	actual, err := provider.PublicationRepositoryBinding(ctx, expected.ImageRepository)
	if err != nil || actual.ImageRepository != expected.ImageRepository || actual.RepositoryID != repo.ID || actual.InstallationID != repo.InstallationID || actual.SourceRepository != repo.FullName || actual.Revision <= 0 {
		return errPublisherUnavailable
	}
	return nil
}

type publisherRepositoryReview struct {
	Repository       publishers.Repository `json:"repository"`
	ImageRepository  string                `json:"imageRepository"`
	PreviousName     string                `json:"previousName"`
	Fingerprint      string                `json:"fingerprint"`
	RequiresApproval bool                  `json:"requiresApproval"`
}

var errPublisherReview = errors.New("publisher_review_required")

func (s *Server) repositoryReview(ctx context.Context, state publishers.Snapshot, index int) (publisherRepositoryReview, updates.PublicationBinding, error) {
	var plan publisherRepositoryReview
	var binding updates.PublicationBinding
	c := state.Connections[index]
	if c.State == "disconnected" || c.Deletion != nil || c.RemoteDisablePending || !c.Configured || !s.connectionView(c).CredentialAvailable {
		return plan, binding, errPublisherReview
	}
	provider, ok := s.publicationReceiver.(publicationBindingProvider)
	if !ok {
		return plan, binding, errPublisherUnavailable
	}
	m := s.publishers
	permission, err := m.provider.CheckPermissions(ctx, appCredentials(state.App))
	if err != nil || !permission.VariablesWrite || state.App.ID <= 0 {
		return plan, binding, errPublisherUnavailable
	}
	repos, err := m.provider.Repositories(ctx, appCredentials(state.App))
	if err != nil {
		return plan, binding, errPublisherUnavailable
	}
	repo, err := authorizedPublisherRepository(ctx, m, state, c, repos)
	if err != nil {
		return plan, binding, err
	}
	binding, err = provider.PublicationRepositoryBinding(ctx, c.ImageRepository)
	if err != nil {
		return plan, binding, errPublisherUnavailable
	}
	if binding.RepositoryID != 0 && (binding.RepositoryID != repo.ID || binding.InstallationID != repo.InstallationID) {
		return plan, binding, errPublisherIdentity
	}
	previous := binding.SourceRepository
	if previous == "" {
		previous = c.Repository.FullName
	}
	if binding.SourceRepository != "" && repositoryOwner(binding.SourceRepository) != repositoryOwner(repo.FullName) {
		return plan, binding, errPublisherIdentity
	}
	plan = publisherRepositoryReview{Repository: storedRepository(repo), ImageRepository: c.ImageRepository, PreviousName: previous, RequiresApproval: binding.SourceRepository != "" && binding.RepositoryID == 0}
	if journal := c.ReconciliationReview; journal != nil {
		expected := updates.PublicationBinding{ImageRepository: c.ImageRepository, SourceRepository: journal.ExpectedSource, RepositoryID: journal.ExpectedRepositoryID, InstallationID: journal.ExpectedInstallationID, Revision: journal.ExpectedRevision}
		matches := s.repositoryReviewMatchesJournal(state, c, expected, plan.Repository)
		alreadyApplied := binding.RepositoryID == repo.ID && binding.InstallationID == repo.InstallationID && binding.SourceRepository == repo.FullName && binding.Revision == expected.Revision+1
		if !matches || (binding != expected && !alreadyApplied) {
			return plan, binding, errPublisherReview
		}
		plan.PreviousName = expected.SourceRepository
		plan.Fingerprint = journal.Fingerprint
		plan.RequiresApproval = true
		return plan, binding, nil
	}
	plan.Fingerprint, err = s.repositoryReviewFingerprint(state, c, binding, plan.Repository)
	return plan, binding, err
}

func (s *Server) repositoryReviewFingerprint(state publishers.Snapshot, c publishers.Connection, binding updates.PublicationBinding, repository publishers.Repository) (string, error) {
	// Discovery polls can temporarily report unavailability without changing
	// trust. PLAN and APPLY verify trust directly; the display status is not
	// evidence. The delivery generation remains bound separately.
	c.Reconciliation = nil
	return s.repositoryReviewFingerprintWithStatus(state, c, binding, repository)
}

func (s *Server) repositoryReviewMatchesJournal(state publishers.Snapshot, c publishers.Connection, binding updates.PublicationBinding, repository publishers.Repository) bool {
	fingerprint, err := s.repositoryReviewFingerprint(state, c, binding, repository)
	if err != nil {
		return false
	}
	if fingerprint == c.ReconciliationReview.Fingerprint {
		return true
	}
	// Preserve recovery for already-durable journals created by older servers.
	// Never accept this older fingerprint for a fresh approval.
	legacy, err := s.repositoryReviewFingerprintWithStatus(state, c, binding, repository)
	return err == nil && legacy == c.ReconciliationReview.Fingerprint
}

func (s *Server) repositoryReviewFingerprintWithStatus(state publishers.Snapshot, c publishers.Connection, binding updates.PublicationBinding, repository publishers.Repository) (string, error) {
	keys, err := s.authStore.ListKeys()
	if err != nil {
		return "", errPublisherUnavailable
	}
	var currentKey, previousKey *auth.APIKey
	for _, key := range keys {
		key.LastUsedAt = ""
		if key.ID == c.KeyID {
			copy := key
			currentKey = &copy
		}
		if key.ID == c.PreviousKeyID {
			copy := key
			previousKey = &copy
		}
	}
	c.ReconciliationReview = nil
	// Receipt completion is observational, including an old in-flight request
	// finishing while the journal is pending. Keep the eligibility generation
	// below in scope, but not delivery evidence or its ordering metadata.
	c.LastPublication = nil
	c.LastPublicationReconciliation = ""
	c.LastPublicationRunID = ""
	c.LastPublicationRunAttempt = 0
	if c.Reconciliation != nil {
		copy := *c.Reconciliation
		copy.CheckedAt = ""
		c.Reconciliation = &copy
	}
	// Bind only this operation's private scope. Unrelated keys, receipts and
	// repository trust must not strand a durable journal after a partial commit.
	// Keep the App PEM explicit: githubapp.App omits it from JSON serialization.
	data, _ := json.Marshal(struct {
		AppID                    int64
		AppSlug, AppName, AppPEM string
		Endpoint                 string
		Tailnet                  publishers.Tailnet
		Connection               publishers.Connection
		CurrentKey, PreviousKey  *auth.APIKey
		Binding                  updates.PublicationBinding
		Repository               publishers.Repository
	}{state.App.ID, state.App.Slug, state.App.Name, state.App.PEM, s.publishers.endpoint, repositoryTailnet(state, c.Repository.ID), c, currentKey, previousKey, binding, repository})
	return digestID(string(data)), nil
}

func publisherReconciliationError(w http.ResponseWriter, err error) {
	status := http.StatusServiceUnavailable
	code := "publisher_unavailable"
	if errors.Is(err, errPublisherIdentity) || errors.Is(err, auth.ErrUnauthorized) {
		status = http.StatusForbidden
		code = "publisher_identity_mismatch"
	}
	if errors.Is(err, errPublisherReview) || errors.Is(err, updates.ErrPublicationConflict) {
		status = http.StatusConflict
		code = "publisher_review_required"
	}
	authJSON(w, status, map[string]string{"error": code})
}

func (s *Server) publisherReconciliationPlan(w http.ResponseWriter, r *http.Request) {
	s.publisherRepositoryReconciliation(w, r, false)
}
func (s *Server) publisherReconciliationApply(w http.ResponseWriter, r *http.Request) {
	s.publisherRepositoryReconciliation(w, r, true)
}
func (s *Server) publisherRepositoryReconciliation(w http.ResponseWriter, r *http.Request, apply bool) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Fingerprint string `json:"fingerprint"`
		Approved    bool   `json:"approved"`
	}
	if apply && !authDecode(w, r, &body) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		publisherReconciliationError(w, errPublisherUnavailable)
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
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	plan, binding, err := s.repositoryReview(ctx, state, index)
	if err != nil {
		publisherReconciliationError(w, err)
		return
	}
	if !apply {
		authJSON(w, 200, plan)
		return
	}
	c := &state.Connections[index]
	if !body.Approved || body.Fingerprint == "" {
		publisherReconciliationError(w, errPublisherReview)
		return
	}
	journal := c.ReconciliationReview
	if journal != nil && journal.Fingerprint == body.Fingerprint {
		expected := updates.PublicationBinding{ImageRepository: c.ImageRepository, SourceRepository: journal.ExpectedSource, RepositoryID: journal.ExpectedRepositoryID, InstallationID: journal.ExpectedInstallationID, Revision: journal.ExpectedRevision}
		alreadyApplied := binding.RepositoryID == plan.Repository.ID && binding.InstallationID == plan.Repository.InstallationID && binding.SourceRepository == plan.Repository.FullName && binding.Revision == expected.Revision+1
		if !s.repositoryReviewMatchesJournal(state, *c, expected, plan.Repository) || (binding != expected && !alreadyApplied) {
			publisherReconciliationError(w, errPublisherReview)
			return
		}
		binding = expected
		plan.PreviousName = expected.SourceRepository
	} else {
		if body.Fingerprint != plan.Fingerprint {
			publisherReconciliationError(w, errPublisherReview)
			return
		}
		journal = &publishers.ReconciliationReview{Fingerprint: body.Fingerprint, ExpectedSource: binding.SourceRepository, ExpectedRepositoryID: binding.RepositoryID, ExpectedInstallationID: binding.InstallationID, ExpectedRevision: binding.Revision, EligibleAfter: time.Now().UTC().Format(time.RFC3339Nano)}
		c.ReconciliationReview = journal
		// Durable approval before the independent publication-store transaction.
		if err = m.store.Save(state); err != nil {
			publisherReconciliationError(w, errPublisherUnavailable)
			return
		}
	}
	provider := s.publicationReceiver.(publicationBindingProvider)
	if err = provider.ReconcilePublicationRepository(ctx, binding, plan.Repository.ID, plan.Repository.InstallationID, plan.Repository.FullName); err != nil {
		publisherReconciliationError(w, err)
		return
	}
	confirmed, err := provider.PublicationRepositoryBinding(ctx, plan.ImageRepository)
	if err != nil || confirmed.RepositoryID != plan.Repository.ID || confirmed.InstallationID != plan.Repository.InstallationID || confirmed.SourceRepository != plan.Repository.FullName {
		publisherReconciliationError(w, errPublisherUnavailable)
		return
	}
	c.RepositoryOwner = repositoryOwner(plan.Repository.FullName)
	c.Repository = plan.Repository
	c.Reconciliation = publisherReconciliation(*c, plan.Repository.FullName, "awaiting_publication")
	c.Reconciliation.PreviousName = plan.PreviousName
	c.Reconciliation.Message = "Repository identity reconciled. Waiting for an eligible image publication; a callback alone is not publication verification."
	c.PublicationEligibleAfter = journal.EligibleAfter
	c.ReconciliationReview = nil
	if err = m.store.Save(state); err != nil {
		publisherReconciliationError(w, errPublisherUnavailable)
		return
	}
	authJSON(w, 200, map[string]any{"connection": s.connectionView(*c)})
}
