package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"services/internal/groups"
	"services/internal/publishers"
	"services/internal/updates"
)

type reconciliationPausedReceipt struct {
	*reconciliationFailAfterCAS
	entered, release chan struct{}
}

func (r *reconciliationPausedReceipt) AcceptPublication(ctx context.Context, p groups.ImagePublication) (string, error) {
	result, err := r.Checker.AcceptPublication(ctx, p)
	close(r.entered)
	select {
	case <-r.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return result, err
}

func TestPublisherReconciliationPendingRecoveryRetainsAwaitingReceiptState(t *testing.T) {
	s, _, original, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, original, cookie)
	state, err := original.Load()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "publisher-pending-receipt")
	store, err := publishers.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	s.publishers.store = store
	history := realPublisherReceiver(t, s)
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(history)
	if _, err := checker.AcceptPublication(context.Background(), reviewPublication(t)); err != nil {
		t.Fatal(err)
	}
	path := "/api/publishers/" + c.ID + "/reconciliation"
	plan := func() publisherRepositoryReview {
		t.Helper()
		w := publisherRequest(s, "POST", path+"/plan", map[string]any{}, cookie, "")
		var p publisherRepositoryReview
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || w.Code != 200 {
			t.Fatalf("PLAN: %d %s", w.Code, w.Body.String())
		}
		return p
	}
	// Establish a real ID-bound, awaiting-publication epoch through explicit review.
	initial := plan()
	if !initial.RequiresApproval {
		t.Fatal("legacy history did not require initial approval")
	}
	if w := publisherRequest(s, "POST", path, map[string]any{"fingerprint": initial.Fingerprint, "approved": true}, cookie, ""); w.Code != 200 {
		t.Fatalf("initial APPLY: %d %s", w.Code, w.Body.String())
	}
	state, err = store.Load()
	if err != nil || state.Connections[0].Reconciliation == nil || state.Connections[0].Reconciliation.State != "awaiting_publication" || state.Connections[0].PublicationEligibleAfter == "" {
		t.Fatalf("initial epoch missing: %+v %v", state, err)
	}
	before := state.Connections[0]
	bindingBefore, err := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
	if err != nil || bindingBefore.RepositoryID != c.Repository.ID || bindingBefore.InstallationID != c.Repository.InstallationID {
		t.Fatalf("initial identity not bound: %+v %v", bindingBefore, err)
	}
	db, err := sql.Open("sqlite", filepath.Join(directory, "publishers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	failure := &reconciliationFailAfterCAS{Checker: checker, db: db, fail: true}
	paused := &reconciliationPausedReceipt{reconciliationFailAfterCAS: failure, entered: make(chan struct{}), release: make(chan struct{})}
	resume := sync.OnceFunc(func() { close(paused.release) })
	t.Cleanup(resume)
	if err := s.ConfigurePublicationReceiver(paused); err != nil {
		t.Fatal(err)
	}
	p := reviewPublication(t)
	p.RunID, p.RunURL = "124", "https://github.com/acme/app/actions/runs/124"
	p.PublishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret) }()
	select {
	case <-paused.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("eligible publication did not reach durable acceptance")
	}
	received, err := history.PublicationEventReceivedAt(context.Background(), p)
	boundary, parseErr := time.Parse(time.RFC3339Nano, before.PublicationEligibleAfter)
	if err != nil || parseErr != nil || received.IsZero() || received.Before(boundary) {
		t.Fatalf("receipt not eligible for original epoch: %v %v %v", received, err, parseErr)
	}
	provider.repositories[0].FullName = "acme/renamed"
	rename := plan()
	if w := publisherRequest(s, "POST", path, map[string]any{"fingerprint": rename.Fingerprint, "approved": true}, cookie, ""); w.Code != 503 {
		t.Fatalf("CAS/final-save failure: %d %s", w.Code, w.Body.String())
	}
	bindingAfter, err := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
	if err != nil || bindingAfter.SourceRepository != "acme/renamed" || bindingAfter.RepositoryID != c.Repository.ID || bindingAfter.Revision != bindingBefore.Revision+1 {
		t.Fatalf("rename CAS did not commit: %+v %v", bindingAfter, err)
	}
	state, err = store.Load()
	if err != nil || state.Connections[0].ReconciliationReview == nil || !reflect.DeepEqual(state.Connections[0].Reconciliation, before.Reconciliation) || state.Connections[0].PublicationEligibleAfter != before.PublicationEligibleAfter {
		t.Fatalf("old awaiting epoch/journal not retained: %+v %v", state, err)
	}
	journal := *state.Connections[0].ReconciliationReview
	if journal.EligibleAfter == before.PublicationEligibleAfter {
		t.Fatal("recovery did not create a new epoch")
	}
	failure.fail = false
	if _, err := db.Exec("DROP TRIGGER fail_reconciliation"); err != nil {
		t.Fatal(err)
	}
	resume()
	if w := <-done; w.Code != 200 {
		t.Fatalf("paused receipt completion: %d %s", w.Code, w.Body.String())
	}
	state, err = store.Load()
	if err != nil || state.Connections[0].LastPublication == nil || state.Connections[0].LastPublicationRunID != p.RunID || state.Connections[0].LastPublicationRunAttempt != p.RunAttempt || state.Connections[0].LastPublicationReconciliation != before.PublicationEligibleAfter || !reflect.DeepEqual(state.Connections[0].ReconciliationReview, &journal) {
		t.Fatalf("legitimate old-epoch evidence/order/journal not retained: %+v %v", state, err)
	}
	t.Logf("pending receipt reconciliation state: %s", state.Connections[0].Reconciliation.State)
	recovery := plan()
	if !recovery.RequiresApproval || recovery.Fingerprint != journal.Fingerprint {
		t.Fatalf("fresh PLAN lost pending approval: %+v", recovery)
	}
	if !reflect.DeepEqual(state.Connections[0].Reconciliation, before.Reconciliation) {
		t.Fatal("pending receipt changed reconciliation state/message")
	}
	if w := publisherRequest(s, "POST", path, map[string]any{"fingerprint": recovery.Fingerprint, "approved": false}, cookie, ""); w.Code != 409 {
		t.Fatalf("recovery bypassed explicit approval: %d", w.Code)
	}
	body := map[string]any{"fingerprint": recovery.Fingerprint, "approved": true}
	if w := publisherRequest(s, "POST", path, body, cookie, ""); w.Code != 200 {
		t.Fatalf("recovery APPLY: %d %s", w.Code, w.Body.String())
	}
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	recovered := saved.Connections[0]
	if recovered.ReconciliationReview != nil || recovered.Repository.FullName != "acme/renamed" || recovered.Reconciliation.State != "awaiting_publication" || recovered.PublicationEligibleAfter != journal.EligibleAfter || !reflect.DeepEqual(recovered.LastPublication, state.Connections[0].LastPublication) || recovered.LastPublicationReconciliation != before.PublicationEligibleAfter || s.connectionView(recovered).LastPublication != nil {
		t.Fatalf("recovered generation discarded or certified old receipt: %+v", recovered)
	}
	readback := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	var status struct{ Connections []publisherConnectionView }
	if err := json.Unmarshal(readback.Body.Bytes(), &status); err != nil || readback.Code != 200 || len(status.Connections) != 1 || status.Connections[0].Reconciliation == nil || status.Connections[0].Reconciliation.State != "awaiting_publication" || status.Connections[0].LastPublication != nil {
		t.Fatalf("recovered status certified old receipt: %d %s", readback.Code, readback.Body.String())
	}
	receivedAfter, err := history.PublicationEventReceivedAt(context.Background(), p)
	if err != nil || !received.Equal(receivedAfter) {
		t.Fatalf("historical acceptance changed: %v %v", receivedAfter, err)
	}
	if binding, err := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository); err != nil || binding != bindingAfter {
		t.Fatalf("recovery changed committed binding: %+v %v", binding, err)
	}
	if w := publisherRequest(s, "POST", path, body, cookie, ""); w.Code != 409 {
		t.Fatalf("consumed approval reused: %d", w.Code)
	}
}

func TestPublisherReconciliationRecoveryIgnoresUnrelatedActivity(t *testing.T) {
	for _, activity := range []string{"read_key", "managed_receipt", "other_tailnet", "unused_global_tailnet", "affected_inflight_receipt"} {
		t.Run(activity, func(t *testing.T) {
			s, keys, original, provider, cookie := publisherServer(t)
			c := connectionForTest(t, s, original, cookie)
			state, err := original.Load()
			if err != nil {
				t.Fatal(err)
			}
			// Give the second managed publisher its own credential and identity.
			other := c
			other.ID = "other-managed-publisher"
			other.Repository.ID = 13
			other.Repository.FullName = "acme/other"
			other.ImageRepository = "ghcr.io/acme/other"
			key, token, err := keys.CreateKey("other-publisher", []string{"publications:write"}, []string{other.ImageRepository}, "")
			if err != nil {
				t.Fatal(err)
			}
			other.KeyID, other.Secret = key.ID, token
			state.Connections = append(state.Connections, other)
			otherRepo := provider.repositories[0]
			otherRepo.ID, otherRepo.FullName = other.Repository.ID, other.Repository.FullName
			provider.repositories = append(provider.repositories, otherRepo)
			state.Tailnets["13"] = state.Tailnets["12"]

			directory := filepath.Join(t.TempDir(), "publisher-recovery-scope")
			store, err := publishers.Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			s.publishers.store = store
			history := realPublisherReceiver(t, s)
			checker := updates.NewChecker(nil, 0)
			checker.SetPublicationStore(history)
			publication := reviewPublication(t)
			if _, err := checker.AcceptPublication(context.Background(), publication); err != nil {
				t.Fatal(err)
			}
			receivedBefore, err := history.PublicationEventReceivedAt(context.Background(), publication)
			if err != nil {
				t.Fatal(err)
			}
			bindingBefore, err := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
			if err != nil {
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
			var receiptDone chan *httptest.ResponseRecorder
			var resumeReceipt func()
			if activity == "affected_inflight_receipt" {
				paused := &reconciliationPausedReceipt{reconciliationFailAfterCAS: receiver, entered: make(chan struct{}), release: make(chan struct{})}
				resumeReceipt = sync.OnceFunc(func() { close(paused.release) })
				t.Cleanup(resumeReceipt)
				if err := s.ConfigurePublicationReceiver(paused); err != nil {
					t.Fatal(err)
				}
				receiptDone = make(chan *httptest.ResponseRecorder, 1)
				go func() {
					receiptDone <- publisherRequest(s, "POST", "/api/images/published", publication, nil, c.Secret)
				}()
				select {
				case <-paused.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("pre-review receipt did not reach durable acceptance")
				}
			}
			provider.repositories[0].FullName = "acme/renamed"
			path := "/api/publishers/" + c.ID + "/reconciliation"
			planResponse := publisherRequest(s, "POST", path+"/plan", map[string]any{}, cookie, "")
			var plan publisherRepositoryReview
			if err := json.Unmarshal(planResponse.Body.Bytes(), &plan); err != nil || planResponse.Code != 200 {
				t.Fatalf("initial plan: %d %s", planResponse.Code, planResponse.Body.String())
			}
			body := map[string]any{"fingerprint": plan.Fingerprint, "approved": true}
			if w := publisherRequest(s, "POST", path, body, cookie, ""); w.Code != 503 {
				t.Fatalf("partial write: %d %s", w.Code, w.Body.String())
			}
			bindingAfter, err := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
			if err != nil || bindingAfter.RepositoryID != c.Repository.ID || bindingAfter.SourceRepository != "acme/renamed" || bindingAfter.Revision != bindingBefore.Revision+1 {
				t.Fatalf("binding CAS did not commit: %+v %v", bindingAfter, err)
			}
			pending, err := store.Load()
			if err != nil || pending.Connections[0].ReconciliationReview == nil || pending.Connections[0].Repository != c.Repository {
				t.Fatalf("durable pending journal missing: %+v %v", pending, err)
			}
			journal := *pending.Connections[0].ReconciliationReview
			receiver.fail = false
			if _, err := db.Exec("DROP TRIGGER fail_reconciliation"); err != nil {
				t.Fatal(err)
			}

			switch activity {
			case "affected_inflight_receipt":
				resumeReceipt()
				w := <-receiptDone
				if w.Code != 200 {
					t.Fatalf("pre-review receipt completion: %d %s", w.Code, w.Body.String())
				}
				saved, err := store.Load()
				if err != nil || saved.Connections[0].LastPublication == nil || saved.Connections[0].ReconciliationReview == nil {
					t.Fatalf("late receipt did not exercise pending journal: %v", err)
				}
			case "read_key":
				if _, _, err := keys.CreateKey("unrelated-reader", []string{"services:read"}, nil, ""); err != nil {
					t.Fatal(err)
				}
			case "managed_receipt":
				p := publication
				p.Repository, p.SourceRepository = other.ImageRepository, other.Repository.FullName
				p.RunURL = "https://github.com/acme/other/actions/runs/" + p.RunID
				if w := publisherRequest(s, "POST", "/api/images/published", p, nil, other.Secret); w.Code != 200 {
					t.Fatalf("unrelated publication: %d %s", w.Code, w.Body.String())
				}
				saved, err := store.Load()
				if err != nil || saved.Connections[1].LastPublication == nil {
					t.Fatalf("second managed receipt not saved: %v", err)
				}
			case "other_tailnet", "unused_global_tailnet":
				saved, err := store.Load()
				if err != nil {
					t.Fatal(err)
				}
				if activity == "other_tailnet" {
					saved.Tailnets["13"] = publishers.Tailnet{ClientID: "unrelated", Audience: "other", Tags: "tag:other"}
				} else {
					saved.Tailnet.ClientID = "unused-global"
				}
				if err := store.Save(saved); err != nil {
					t.Fatal(err)
				}
			}
			fresh := publisherRequest(s, "POST", path+"/plan", map[string]any{}, cookie, "")
			var recovery publisherRepositoryReview
			if err := json.Unmarshal(fresh.Body.Bytes(), &recovery); err != nil || fresh.Code != 200 || !recovery.RequiresApproval || recovery.Fingerprint != journal.Fingerprint {
				t.Fatalf("unrelated %s stranded fresh recovery PLAN: %d %s", activity, fresh.Code, fresh.Body.String())
			}
			if w := publisherRequest(s, "POST", path, map[string]any{"fingerprint": recovery.Fingerprint, "approved": false}, cookie, ""); w.Code != 409 {
				t.Fatalf("recovery skipped explicit consent: %d", w.Code)
			}
			body = map[string]any{"fingerprint": recovery.Fingerprint, "approved": true}
			if w := publisherRequest(s, "POST", path, body, cookie, ""); w.Code != 200 {
				t.Fatalf("recovery APPLY: %d %s", w.Code, w.Body.String())
			}
			saved, err := store.Load()
			if err != nil || len(saved.Connections) != 2 {
				t.Fatalf("connections not retained: %v", err)
			}
			recovered := saved.Connections[0]
			if recovered.ID != c.ID || recovered.KeyID != c.KeyID || recovered.Secret != c.Secret || recovered.ReconciliationReview != nil || recovered.Reconciliation == nil || recovered.Reconciliation.State != "awaiting_publication" || recovered.PublicationEligibleAfter != journal.EligibleAfter {
				t.Fatalf("original connection/journal not recovered: %+v", recovered)
			}
			if activity == "affected_inflight_receipt" && (recovered.LastPublication == nil || s.connectionView(recovered).LastPublication != nil) {
				t.Fatal("late receipt was discarded or certified the new generation")
			}
			receivedAfter, err := history.PublicationEventReceivedAt(context.Background(), publication)
			if err != nil || receivedBefore.IsZero() || !reflect.DeepEqual(receivedBefore, receivedAfter) {
				t.Fatalf("retained history changed: %v %v %v", receivedBefore, receivedAfter, err)
			}
			bindingRecovered, err := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
			if err != nil || bindingRecovered != bindingAfter {
				t.Fatalf("retry mutated committed binding: %+v %v", bindingRecovered, err)
			}
			if w := publisherRequest(s, "POST", path, body, cookie, ""); w.Code != 409 {
				t.Fatalf("consumed fingerprint reused: %d", w.Code)
			}
		})
	}
}
