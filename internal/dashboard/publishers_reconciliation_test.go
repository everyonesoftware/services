package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"services/internal/updates"
	"testing"
)

func realPublisherReceiver(t *testing.T, s *Server) *updates.PublicationStore {
	t.Helper()
	store, err := updates.OpenPublicationStore(filepath.Join(t.TempDir(), "publications.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(store)
	if err := s.ConfigurePublicationReceiver(checker); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestPublisherReviewedReconciliationRetainsLegacyHistory(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	history := realPublisherReceiver(t, s)
	p := reviewPublication(t)
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(history)
	if _, err := checker.AcceptPublication(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	provider.repositories[0].FullName = "acme/renamed"
	publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	w := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/reconciliation/plan", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
	var plan struct {
		PreviousName, Fingerprint string
		RequiresApproval          bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.PreviousName != "acme/app" || !plan.RequiresApproval || len(plan.Fingerprint) != 64 {
		t.Fatalf("unsafe plan: %s", w.Body.String())
	}
	binding, _ := history.PublicationRepositoryBinding(context.Background(), p.Repository)
	if binding.RepositoryID != 0 || binding.SourceRepository != "acme/app" {
		t.Fatal("plan mutated history", binding)
	}
	path := "/api/publishers/" + c.ID + "/reconciliation"
	if w := publisherRequest(s, "POST", path, map[string]any{"fingerprint": plan.Fingerprint}, cookie, ""); w.Code != 409 {
		t.Fatal("missing consent accepted", w.Code)
	}
	// An observational poll must not invalidate the exact same review.
	publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	w = publisherRequest(s, "POST", path, map[string]any{"fingerprint": plan.Fingerprint, "approved": true}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	binding, _ = history.PublicationRepositoryBinding(context.Background(), p.Repository)
	if binding.RepositoryID != 12 || binding.InstallationID != 7 || binding.SourceRepository != "acme/renamed" {
		t.Fatal("binding not migrated", binding)
	}
	if _, err := checker.AcceptPublication(context.Background(), p); err == nil {
		t.Fatal("old-name replay unexpectedly accepted")
	}
}

func TestPublisherReviewSurvivesTransientStatusFailure(t *testing.T) {
	s, _, store, base, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	history := realPublisherReceiver(t, s)
	p := reviewPublication(t)
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(history)
	if _, err := checker.AcceptPublication(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	provider := &repositoryDiscoveryProvider{publisherProviderFake: base}
	s.publishers.provider = provider
	base.repositories[0].FullName = "acme/renamed"
	publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	path := "/api/publishers/" + c.ID + "/reconciliation"
	w := publisherRequest(s, "POST", path+"/plan", map[string]any{}, cookie, "")
	var plan publisherRepositoryReview
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil || w.Code != http.StatusOK || !plan.RequiresApproval {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
	provider.verifyErr = context.DeadlineExceeded
	publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	state, err := store.Load()
	if err != nil || state.Connections[0].Reconciliation.State != "unavailable" {
		t.Fatalf("status failure was not saved: %v", err)
	}
	payload := map[string]any{"approved": true, "fingerprint": plan.Fingerprint}
	if w := publisherRequest(s, "POST", path, payload, cookie, ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("apply must still require fresh authorization: %d %s", w.Code, w.Body.String())
	}
	provider.verifyErr = nil
	if w := publisherRequest(s, "POST", path, payload, cookie, ""); w.Code != http.StatusOK {
		t.Fatalf("unchanged authorized review rejected after observational status poll: %d %s", w.Code, w.Body.String())
	}
	b, err := history.PublicationRepositoryBinding(context.Background(), p.Repository)
	if err != nil || b.SourceRepository != "acme/renamed" || b.RepositoryID != 12 || b.InstallationID != 7 {
		t.Fatalf("binding not reconciled: %+v %v", b, err)
	}
}

func TestPublisherNewConnectionRetainsOriginalOwnerBoundary(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	connectionForTest(t, s, store, cookie)
	state, _ := store.Load()
	if state.Connections[0].RepositoryOwner != "acme" {
		t.Fatal("original repository owner was not retained")
	}
}

func TestPublisherStatusDiscoversRenameWithoutAuthorizingLegacyHistory(t *testing.T) {
	s, keys, store, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	before, _ := keys.ListKeys()
	provider.repositories[0].FullName = "acme/renamed"
	w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Connections []struct {
			Repository struct {
				FullName string `json:"fullName"`
			}
			Reconciliation *struct{ State, PreviousName, CurrentName string }
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	v := response.Connections[0]
	if v.Reconciliation == nil || v.Reconciliation.State != "review_required" || v.Reconciliation.PreviousName != c.Repository.FullName || v.Reconciliation.CurrentName != "acme/renamed" {
		t.Fatalf("rename not safely discovered: %s", w.Body.String())
	}
	saved, _ := store.Load()
	if saved.Connections[0].Repository.FullName != "acme/renamed" {
		t.Fatal("current authorized name not refreshed")
	}
	after, _ := keys.ListKeys()
	if len(before) != len(after) {
		t.Fatal("status changed credentials")
	}
	for i := range before {
		if before[i].LastUsedAt != after[i].LastUsedAt {
			t.Fatal("status authenticated credential")
		}
	}
}

func TestPublisherStatusOwnerTransferAndLostIdentityAreBlocked(t *testing.T) {
	for _, scenario := range []string{"transfer", "replacement", "lost"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, store, provider, cookie := publisherServer(t)
			c := connectionForTest(t, s, store, cookie)
			switch scenario {
			case "transfer":
				provider.repositories[0].FullName = "other/app"
			case "replacement":
				provider.repositories[0].ID++
			case "lost":
				provider.repositories = nil
			}
			w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
			var response map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &response)
			row := response["connections"].([]any)[0].(map[string]any)
			reconciliation, ok := row["reconciliation"].(map[string]any)
			if !ok || reconciliation["state"] != "identity_mismatch" {
				t.Fatalf("identity not blocked: %s", w.Body.String())
			}
			saved, _ := store.Load()
			if saved.Connections[0].Repository.FullName != c.Repository.FullName {
				t.Fatal("untrusted owner changed")
			}
		})
	}
}
