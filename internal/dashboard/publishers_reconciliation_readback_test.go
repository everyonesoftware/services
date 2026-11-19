package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"services/internal/updates"
)

type bindingReadbackFailure struct {
	*updates.Checker
	fail    bool
	changed bool
}

func (r *bindingReadbackFailure) PublicationRepositoryBinding(ctx context.Context, image string) (updates.PublicationBinding, error) {
	if r.changed && r.fail {
		return updates.PublicationBinding{}, errors.New("private provider or store diagnostic")
	}
	return r.Checker.PublicationRepositoryBinding(ctx, image)
}
func (r *bindingReadbackFailure) ReconcilePublicationRepository(ctx context.Context, b updates.PublicationBinding, id, installation int64, name string) error {
	err := r.Checker.ReconcilePublicationRepository(ctx, b, id, installation, name)
	if err == nil {
		r.changed = true
	}
	return err
}
func TestPublisherAutomaticRenameRequiresBindingReadbackAndRecovers(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	history := realPublisherReceiver(t, s)
	ctx := context.Background()
	b, _ := history.PublicationRepositoryBinding(ctx, c.ImageRepository)
	if err := history.ReconcilePublicationRepository(ctx, b, 12, 7, "acme/app"); err != nil {
		t.Fatal(err)
	}
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(history)
	receiver := &bindingReadbackFailure{Checker: checker, fail: true}
	if err := s.ConfigurePublicationReceiver(receiver); err != nil {
		t.Fatal(err)
	}
	provider.repositories[0].FullName = "acme/renamed"
	read := func(want string) {
		t.Helper()
		w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
		var response struct{ Connections []publisherConnectionView }
		_ = json.Unmarshal(w.Body.Bytes(), &response)
		if response.Connections[0].Reconciliation == nil || response.Connections[0].Reconciliation.State != want {
			t.Fatalf("readback state want %s: %s", want, w.Body.String())
		}
	}
	read("unavailable")
	receiver.fail = false
	read("awaiting_publication")
}
