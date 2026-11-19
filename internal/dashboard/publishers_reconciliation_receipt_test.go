package dashboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"net/http/httptest"
	"services/internal/groups"
	"services/internal/updates"
)

func TestPublisherBoundRenameAutomaticallyReconcilesAndRequiresFreshReceipt(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	history := realPublisherReceiver(t, s)
	binding, _ := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
	if err := history.ReconcilePublicationRepository(context.Background(), binding, 12, 7, "acme/app"); err != nil {
		t.Fatal(err)
	}
	p := reviewPublication(t)
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	provider.repositories[0].FullName = "acme/renamed"
	w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	binding, _ = history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
	if binding.SourceRepository != "acme/renamed" {
		t.Fatal("stable bound rename not reconciled", binding)
	}
	var response struct{ Connections []publisherConnectionView }
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if response.Connections[0].LastPublication != nil || response.Connections[0].Reconciliation.State != "awaiting_publication" {
		t.Fatalf("old receipt presented as current delivery: %s", w.Body.String())
	}
	// A payload captured before the rename cannot re-certify delivery by changing its source.
	p.SourceRepository = "acme/renamed"
	p.RunURL = "https://github.com/acme/renamed/actions/runs/124"
	p.RunID = "124"
	p.PublishedAt = "2026-11-09T13:00:00Z"
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ := store.Load()
	if saved.Connections[0].Reconciliation.State != "awaiting_publication" {
		t.Fatal("pre-rename publication certified reconciliation")
	}
	p.PublishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	p.RunID = "125"
	p.RunURL = "https://github.com/acme/renamed/actions/runs/125"
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, _ = store.Load()
	if saved.Connections[0].Reconciliation.State != "reconciled" {
		t.Fatal("fresh receipt did not complete reconciliation")
	}
}

func TestPublisherRenameHidesPreRenameReceiptEvenWithFuturePublicationClock(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	history := realPublisherReceiver(t, s)
	binding, _ := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
	if err := history.ReconcilePublicationRepository(context.Background(), binding, 12, 7, "acme/app"); err != nil {
		t.Fatal(err)
	}
	p := reviewPublication(t)
	p.PublishedAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	provider.repositories[0].FullName = "acme/renamed"
	w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	var response struct{ Connections []publisherConnectionView }
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if response.Connections[0].LastPublication != nil {
		t.Fatalf("pre-rename receipt survived reconciliation: %s", w.Body.String())
	}
}

type pausedBoundPublicationReceiver struct {
	*updates.Checker
	entered, release chan struct{}
}

func (r *pausedBoundPublicationReceiver) AcceptPublication(ctx context.Context, p groups.ImagePublication) (string, error) {
	result, err := r.Checker.AcceptPublication(ctx, p)
	close(r.entered)
	<-r.release
	return result, err
}
func TestPublisherReconciliationDoesNotWaitForReceiverOrCertifyItsOldReceipt(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	history := realPublisherReceiver(t, s)
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(history)
	receiver := &pausedBoundPublicationReceiver{Checker: checker, entered: make(chan struct{}), release: make(chan struct{})}
	if err := s.ConfigurePublicationReceiver(receiver); err != nil {
		t.Fatal(err)
	}
	p := reviewPublication(t)
	p.PublishedAt = time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret) }()
	<-receiver.entered
	provider.repositories[0].FullName = "acme/renamed"
	status := make(chan *httptest.ResponseRecorder, 1)
	go func() { status <- publisherRequest(s, "GET", "/api/publishers", nil, cookie, "") }()
	select {
	case w := <-status:
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(3 * time.Second):
		close(receiver.release)
		<-done
		t.Fatal("receiver blocked publisher integration gate")
	}
	close(receiver.release)
	w := <-done
	if w.Code != 503 {
		t.Fatalf("old receipt returned success %d %s", w.Code, w.Body.String())
	}
	state, _ := store.Load()
	if state.Connections[0].LastPublication != nil || state.Connections[0].Reconciliation.State != "awaiting_publication" {
		t.Fatal("paused pre-rename receipt certified new name")
	}
}

func TestPublisherStatusFindsHistoricalNameAfterConnectionReadd(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	history := realPublisherReceiver(t, s)
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(history)
	if _, err := checker.AcceptPublication(context.Background(), reviewPublication(t)); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load()
	state.Connections[0].Repository.FullName = "acme/renamed"
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	provider.repositories[0].FullName = "acme/renamed"
	w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	var response struct{ Connections []publisherConnectionView }
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if response.Connections[0].Reconciliation == nil || response.Connections[0].Reconciliation.PreviousName != "acme/app" || response.Connections[0].Reconciliation.State != "review_required" {
		t.Fatalf("retained binding hidden by new connection %s (%s)", w.Body.String(), c.ID)
	}
}

func TestIndependentReviewUnchangedPayloadBypassesProviderRevocation(t *testing.T) {
	for _, scenario := range []string{"transfer", "replacement", "installation", "lost", "permissions", "discovery", "verify"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, store, provider, cookie := publisherServer(t)
			c := connectionForTest(t, s, store, cookie)
			history := realPublisherReceiver(t, s)
			b, _ := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
			if err := history.ReconcilePublicationRepository(context.Background(), b, 12, 7, "acme/app"); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "transfer":
				provider.repositories[0].FullName = "other/app"
			case "replacement":
				provider.repositories[0].ID++
			case "installation":
				provider.repositories[0].InstallationID++
			case "lost":
				provider.repositories = nil
			case "permissions", "discovery", "verify":
				p := &repositoryDiscoveryProvider{publisherProviderFake: provider}
				switch scenario {
				case "permissions":
					p.permissionsErr = errPublisherUnavailable
				case "discovery":
					p.repositoriesErr = errPublisherUnavailable
				case "verify":
					p.verifyErr = errPublisherUnavailable
				}
				s.publishers.provider = p
			}
			w := publisherRequest(s, "POST", "/api/images/published", reviewPublication(t), nil, c.Secret)
			if w.Code == 200 {
				t.Fatalf("revoked provider authority accepted unchanged payload: %d %s", w.Code, w.Body.String())
			}
			if scenario == "permissions" || scenario == "discovery" || scenario == "verify" {
				if w.Code != 503 {
					t.Fatalf("provider failure not unavailable: %d %s", w.Code, w.Body.String())
				}
				state, _ := store.Load()
				if state.Connections[0].KeyID != c.KeyID || state.Connections[0].Secret != c.Secret || state.Connections[0].LastPublication != nil {
					t.Fatal("provider failure changed saved credential or certified delivery")
				}
			}
		})
	}
}

func TestIndependentReviewOldInFlightReceiptCrossesSameNameReviewEpoch(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	history := realPublisherReceiver(t, s)
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(history)
	// Existing legacy history; no stable binding has been approved yet.
	if _, err := checker.AcceptPublication(context.Background(), reviewPublication(t)); err != nil {
		t.Fatal(err)
	}
	receiver := &pausedBoundPublicationReceiver{Checker: checker, entered: make(chan struct{}), release: make(chan struct{})}
	if err := s.ConfigurePublicationReceiver(receiver); err != nil {
		t.Fatal(err)
	}
	p := reviewPublication(t)
	p.RunID = "124"
	p.RunURL = "https://github.com/acme/app/actions/runs/124"
	p.PublishedAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret) }()
	<-receiver.entered
	path := "/api/publishers/" + c.ID + "/reconciliation"
	w := publisherRequest(s, "POST", path+"/plan", map[string]any{}, cookie, "")
	var plan publisherRepositoryReview
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !plan.RequiresApproval {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
	w = publisherRequest(s, "POST", path, map[string]any{"approved": true, "fingerprint": plan.Fingerprint}, cookie, "")
	close(receiver.release)
	receipt := <-done
	if w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	if receipt.Code != 200 {
		t.Fatalf("receipt: %d %s", receipt.Code, receipt.Body.String())
	}
	state, _ := store.Load()
	if state.Connections[0].Reconciliation.State == "reconciled" {
		t.Fatal("pre-review in-flight receipt was relabeled with the new epoch and certified reconciliation")
	}
}
