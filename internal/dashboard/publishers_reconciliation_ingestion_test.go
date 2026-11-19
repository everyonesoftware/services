package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"services/internal/updates"
	"strings"
	"testing"
)

func TestPublisherNoHistoryCanInitiallyBindAfterAuthorizedRenameWithoutReview(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	history := realPublisherReceiver(t, s)
	provider.repositories[0].FullName = "acme/renamed"
	status := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	var view struct{ Connections []publisherConnectionView }
	if err := json.Unmarshal(status.Body.Bytes(), &view); err != nil || status.Code != 200 || len(view.Connections) != 1 || view.Connections[0].Reconciliation == nil || view.Connections[0].Reconciliation.State != "awaiting_publication" {
		t.Fatalf("absent history stranded in unapprovable review: %d %s", status.Code, status.Body.String())
	}
	p := reviewPublication(t)
	p.SourceRepository = "acme/renamed"
	p.RunURL = "https://github.com/acme/renamed/actions/runs/123"
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	binding, _ := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
	state, _ := store.Load()
	if binding.RepositoryID != 12 || binding.InstallationID != 7 || state.Connections[0].Reconciliation.State != "awaiting_publication" || strings.Contains(state.Connections[0].Reconciliation.Message, "requires explicit") {
		t.Fatal("initial binding incorrectly requires legacy approval", binding, state.Connections[0].Reconciliation)
	}
}

func TestPublisherLegacyReceiverCannotBypassReviewAfterStatusNameRefresh(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	provider.repositories[0].FullName = "acme/renamed"
	publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	p := reviewPublication(t)
	p.SourceRepository = "acme/renamed"
	p.RunURL = "https://github.com/acme/renamed/actions/runs/123"
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret); w.Code != 409 {
		t.Fatalf("metadata refresh silently authorized provenance: %d %s", w.Code, w.Body.String())
	}
}

func TestPublisherPublicationUnavailableReturnsSafeCategory(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	if err := s.ConfigurePublicationReceiver(&publicationReceiver{err: errors.New("sensitive provider credential diagnostic")}); err != nil {
		t.Fatal(err)
	}
	w := publisherRequest(s, "POST", "/api/images/published", reviewPublication(t), nil, c.Secret)
	var response struct{ Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if w.Code != 503 || response.Error != "publication_unavailable" || strings.Contains(w.Body.String(), "sensitive") {
		t.Fatalf("unsafe unavailable category %d %s", w.Code, w.Body.String())
	}
}

func TestPublisherPublicationConflictReturnsSafeCategory(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	realPublisherReceiver(t, s)
	p := reviewPublication(t)
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p.Digest = "sha256:" + strings.Repeat("c", 64)
	w := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret)
	var response struct{ Error string }
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if w.Code != 409 || response.Error != "publication_conflict" {
		t.Fatalf("conflict lacks safe category: %d %s", w.Code, w.Body.String())
	}
}

func TestPublisherIngestionResolvesRenameUsingProviderNotPayloadIdentity(t *testing.T) {
	for _, scenario := range []string{"bound", "legacy", "wrong_id", "transfer", "installation", "replacement", "lost"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, store, provider, cookie := publisherServer(t)
			c := connectionForTest(t, s, store, cookie)
			history := realPublisherReceiver(t, s)
			ctx := context.Background()
			if scenario != "legacy" {
				binding, _ := history.PublicationRepositoryBinding(ctx, c.ImageRepository)
				if err := history.ReconcilePublicationRepository(ctx, binding, 12, 7, "acme/app"); err != nil {
					t.Fatal(err)
				}
			} else {
				checker := updates.NewChecker(nil, 0)
				checker.SetPublicationStore(history)
				if _, err := checker.AcceptPublication(ctx, reviewPublication(t)); err != nil {
					t.Fatal(err)
				}
			}
			provider.repositories[0].FullName = "acme/renamed"
			p := reviewPublication(t)
			p.SourceRepository = "acme/renamed"
			p.RunURL = "https://github.com/acme/renamed/actions/runs/124"
			p.RunID = "124"
			var payload map[string]any
			raw, _ := json.Marshal(p)
			_ = json.Unmarshal(raw, &payload)
			payload["sourceRepositoryId"] = "12"
			want := 200
			switch scenario {
			case "legacy":
				want = 409
			case "wrong_id":
				payload["sourceRepositoryId"] = "999"
				want = 403
			case "transfer":
				provider.repositories[0].FullName = "other/renamed"
				want = 403
			case "installation":
				provider.repositories[0].InstallationID++
				want = 403
			case "replacement":
				provider.repositories[0].ID++
				want = 403
			case "lost":
				provider.repositories = nil
				want = 403
			}
			w := publisherRequest(s, "POST", "/api/images/published", payload, nil, c.Secret)
			if w.Code != want {
				t.Fatalf("ingestion %d want %d: %s", w.Code, want, w.Body.String())
			}
			binding, _ := history.PublicationRepositoryBinding(ctx, c.ImageRepository)
			if scenario != "bound" && binding.SourceRepository != "acme/app" {
				t.Fatal("rejected payload migrated binding", binding)
			}
			if scenario == "legacy" {
				saved, _ := store.Load()
				if saved.Connections[0].Reconciliation == nil || saved.Connections[0].Reconciliation.State != "review_required" {
					t.Fatal("legacy conflict omitted attention")
				}
			}
		})
	}
}
