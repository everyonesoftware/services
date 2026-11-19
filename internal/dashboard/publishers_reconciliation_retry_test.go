package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"services/internal/publishers"
	"services/internal/updates"
)

type reconciliationFailAfterCAS struct {
	*updates.Checker
	db   *sql.DB
	fail bool
}

func (r *reconciliationFailAfterCAS) ReconcilePublicationRepository(ctx context.Context, b updates.PublicationBinding, id, installation int64, name string) error {
	if err := r.Checker.ReconcilePublicationRepository(ctx, b, id, installation, name); err != nil {
		return err
	}
	if r.fail {
		_, err := r.db.Exec(`CREATE TRIGGER fail_reconciliation BEFORE UPDATE ON publisher_state BEGIN SELECT RAISE(FAIL,'injected write failure'); END`)
		return err
	}
	return nil
}

func TestPublisherReconciliationRetryAfterBindingCASAndStateWriteFailure(t *testing.T) {
	s, _, original, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, original, cookie)
	state, _ := original.Load()
	directory := filepath.Join(t.TempDir(), "publisher-retry")
	store, err := publishers.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.publishers.store = store
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	history := realPublisherReceiver(t, s)
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(history)
	if _, err := checker.AcceptPublication(context.Background(), reviewPublication(t)); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(directory, "publishers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	receiver := &reconciliationFailAfterCAS{Checker: checker, db: db, fail: true}
	if err := s.ConfigurePublicationReceiver(receiver); err != nil {
		t.Fatal(err)
	}
	provider.repositories[0].FullName = "acme/renamed"
	planResponse := publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/reconciliation/plan", map[string]any{}, cookie, "")
	var plan struct{ Fingerprint string }
	_ = json.Unmarshal(planResponse.Body.Bytes(), &plan)
	path := "/api/publishers/" + c.ID + "/reconciliation"
	body := map[string]any{"fingerprint": plan.Fingerprint, "approved": true}
	if w := publisherRequest(s, "POST", path, body, cookie, ""); w.Code != 503 {
		t.Fatalf("expected partial failure %d %s", w.Code, w.Body.String())
	}
	binding, _ := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
	if binding.RepositoryID != 12 || binding.SourceRepository != "acme/renamed" {
		t.Fatal("did not exercise committed binding", binding)
	}
	if w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, ""); w.Code != 200 {
		t.Fatalf("status during partial write: %d", w.Code)
	} else {
		var response struct{ Connections []publisherConnectionView }
		_ = json.Unmarshal(w.Body.Bytes(), &response)
		if response.Connections[0].Reconciliation == nil || response.Connections[0].Reconciliation.State != "review_required" || response.Connections[0].LastPublication != nil {
			t.Fatalf("pending reconciliation looks healthy %s", w.Body.String())
		}
	}
	pending := reviewPublication(t)
	pending.SourceRepository = "acme/renamed"
	pending.RunURL = "https://github.com/acme/renamed/actions/runs/123"
	if w := publisherRequest(s, "POST", "/api/images/published", pending, nil, c.Secret); w.Code != 409 {
		t.Fatalf("pending reconciliation accepted ingestion %d %s", w.Code, w.Body.String())
	}
	receiver.fail = false
	if _, err := db.Exec("DROP TRIGGER fail_reconciliation"); err != nil {
		t.Fatal(err)
	}
	if w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, ""); w.Code != 200 {
		t.Fatalf("status during retry %d", w.Code)
	}
	fresh := publisherRequest(s, "POST", path+"/plan", map[string]any{}, cookie, "")
	var recoveryPlan publisherRepositoryReview
	if err := json.Unmarshal(fresh.Body.Bytes(), &recoveryPlan); err != nil || fresh.Code != 200 || !recoveryPlan.RequiresApproval || recoveryPlan.Fingerprint != plan.Fingerprint {
		t.Fatalf("fresh plan lost pending reviewed journal: %d %s", fresh.Code, fresh.Body.String())
	}
	body = map[string]any{"fingerprint": recoveryPlan.Fingerprint, "approved": true}
	if w := publisherRequest(s, "POST", path, body, cookie, ""); w.Code != 200 {
		t.Fatalf("reviewed partial write not retryable: %d %s", w.Code, w.Body.String())
	}
	// Read saved status, rather than treating the successful apply as verification.
	readback := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	var status struct{ Connections []publisherConnectionView }
	if err := json.Unmarshal(readback.Body.Bytes(), &status); err != nil || readback.Code != 200 || len(status.Connections) != 1 || status.Connections[0].Reconciliation == nil || status.Connections[0].Reconciliation.State != "awaiting_publication" {
		t.Fatalf("completed recovery missing from status: %d %s", readback.Code, readback.Body.String())
	}
	// Once complete the approval is consumed, not a blanket permission to reconcile later.
	if w := publisherRequest(s, "POST", path, body, cookie, ""); w.Code != 409 {
		t.Fatalf("consumed approval reused: %d", w.Code)
	}
}
