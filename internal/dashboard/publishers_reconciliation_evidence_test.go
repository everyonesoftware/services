package dashboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"services/internal/updates"
)

func TestPublisherDuplicateAcceptedBeforeSameNameMigrationCannotCertify(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	history := realPublisherReceiver(t, s)
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(history)
	p := reviewPublication(t)
	p.PublishedAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	if _, err := checker.AcceptPublication(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	path := "/api/publishers/" + c.ID + "/reconciliation"
	w := publisherRequest(s, "POST", path+"/plan", map[string]any{}, cookie, "")
	var plan publisherRepositoryReview
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil || w.Code != 200 || !plan.RequiresApproval {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
	w = publisherRequest(s, "POST", path, map[string]any{"approved": true, "fingerprint": plan.Fingerprint}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("apply: %d %s", w.Code, w.Body.String())
	}
	w = publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret)
	if w.Code != 200 {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body.String())
	}
	state, _ := store.Load()
	if state.Connections[0].Reconciliation.State != "awaiting_publication" || state.Connections[0].LastPublication != nil {
		t.Fatal("old duplicate was assigned the migration epoch")
	}
	// A durable acceptance after the migration may repair a lost dashboard save.
	p.RunID = "124"
	p.RunURL = "https://github.com/acme/app/actions/runs/124"
	if _, err := checker.AcceptPublication(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	w = publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret)
	if w.Code != 200 {
		t.Fatalf("eligible duplicate: %d %s", w.Code, w.Body.String())
	}
	state, _ = store.Load()
	if state.Connections[0].Reconciliation.State != "reconciled" || state.Connections[0].LastPublication == nil {
		t.Fatal("durable post-migration duplicate could not repair missing receipt")
	}
}
