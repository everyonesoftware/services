package dashboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"services/internal/publishers"
	"services/internal/updates"
)

func TestPublisherPendingReviewPlanRevalidatesOriginalJournal(t *testing.T) {
	for _, scenario := range []string{"expected", "next_revision", "legacy_fingerprint", "branch", "key", "deletion", "state", "revision", "owner", "provider"} {
		t.Run(scenario, func(t *testing.T) {
			s, keys, store, base, cookie := publisherServer(t)
			c := connectionForTest(t, s, store, cookie)
			history := realPublisherReceiver(t, s)
			checker := updates.NewChecker(nil, 0)
			checker.SetPublicationStore(history)
			if _, err := checker.AcceptPublication(context.Background(), reviewPublication(t)); err != nil {
				t.Fatal(err)
			}
			base.repositories[0].FullName = "acme/renamed"
			publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
			path := "/api/publishers/" + c.ID + "/reconciliation/plan"
			w := publisherRequest(s, "POST", path, map[string]any{}, cookie, "")
			var plan publisherRepositoryReview
			if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil || w.Code != 200 {
				t.Fatalf("plan: %d %s", w.Code, w.Body.String())
			}
			b, err := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
			if err != nil {
				t.Fatal(err)
			}
			state, _ := store.Load()
			if scenario == "legacy_fingerprint" {
				legacy, err := s.repositoryReviewFingerprintWithStatus(state, state.Connections[0], b, plan.Repository)
				if err != nil || legacy == plan.Fingerprint {
					t.Fatalf("fixture needs a distinct legacy fingerprint: %v", err)
				}
				plan.Fingerprint = legacy
			}
			state.Connections[0].ReconciliationReview = &publishers.ReconciliationReview{Fingerprint: plan.Fingerprint, ExpectedSource: b.SourceRepository, ExpectedRepositoryID: b.RepositoryID, ExpectedInstallationID: b.InstallationID, ExpectedRevision: b.Revision, EligibleAfter: time.Now().UTC().Format(time.RFC3339Nano)}
			if scenario != "expected" {
				if err := history.ReconcilePublicationRepository(context.Background(), b, 12, 7, "acme/renamed"); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "branch":
				base.repositories[0].DefaultBranch = "next"
			case "key":
				if err := keys.RevokeKey(c.KeyID); err != nil {
					t.Fatal(err)
				}
			case "deletion":
				state.Connections[0].Deletion = &publishers.Deletion{}
			case "state":
				state.Connections[0].State = "error"
			case "revision":
				latest, _ := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
				if err := history.ReconcilePublicationRepository(context.Background(), latest, 12, 7, "acme/other"); err != nil {
					t.Fatal(err)
				}
				latest, _ = history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
				if err := history.ReconcilePublicationRepository(context.Background(), latest, 12, 7, "acme/renamed"); err != nil {
					t.Fatal(err)
				}
			case "owner":
				base.repositories[0].FullName = "other/renamed"
			case "provider":
				s.publishers.provider = &repositoryDiscoveryProvider{publisherProviderFake: base, repositoriesErr: errPublisherUnavailable}
			}
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			w = publisherRequest(s, "POST", path, map[string]any{}, cookie, "")
			if scenario != "expected" && scenario != "next_revision" && scenario != "legacy_fingerprint" {
				if w.Code == 200 {
					t.Fatalf("changed trust exposed reusable pending approval: %s", w.Body.String())
				}
				return
			}
			var recovery publisherRepositoryReview
			if err := json.Unmarshal(w.Body.Bytes(), &recovery); err != nil || w.Code != 200 || !recovery.RequiresApproval || recovery.Fingerprint != plan.Fingerprint || recovery.PreviousName != "acme/app" {
				t.Fatalf("pending plan: %d %s", w.Code, w.Body.String())
			}
			if scenario == "legacy_fingerprint" {
				apply := "/api/publishers/" + c.ID + "/reconciliation"
				w := publisherRequest(s, "POST", apply, map[string]any{"fingerprint": recovery.Fingerprint, "approved": true}, cookie, "")
				if w.Code != 200 {
					t.Fatalf("legacy journal apply: %d %s", w.Code, w.Body.String())
				}
			}
		})
	}
}
