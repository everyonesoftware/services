package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"services/internal/githubapp"
	"services/internal/publishers"
)

type repositoryDiscoveryProvider struct {
	*publisherProviderFake
	calls                                      int
	permissionsErr, repositoriesErr, verifyErr error
	sawDeadline                                bool
}

func (p *repositoryDiscoveryProvider) CheckPermissions(ctx context.Context, a githubapp.App) (githubapp.PermissionStatus, error) {
	if p.permissionsErr != nil {
		return githubapp.PermissionStatus{}, p.permissionsErr
	}
	return p.publisherProviderFake.CheckPermissions(ctx, a)
}
func (p *repositoryDiscoveryProvider) Repositories(ctx context.Context, a githubapp.App) ([]githubapp.Repository, error) {
	p.calls++
	_, p.sawDeadline = ctx.Deadline()
	if p.repositoriesErr != nil {
		return nil, p.repositoriesErr
	}
	return p.repositories, nil
}
func (p *repositoryDiscoveryProvider) VerifyRepositoryPermissions(context.Context, githubapp.App, githubapp.Repository) error {
	return p.verifyErr
}

func TestPublisherStatusAuthorizationFailureIsNotHealthyAndDiscoveryIsBatched(t *testing.T) {
	for _, scenario := range []string{"batch", "permissions", "discovery", "verify"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, store, base, cookie := publisherServer(t)
			connectionForTest(t, s, store, cookie)
			p := &repositoryDiscoveryProvider{publisherProviderFake: base}
			s.publishers.provider = p
			providerDiagnostic := errors.New("sensitive provider diagnostic access_token=secret")
			switch scenario {
			case "permissions":
				p.permissionsErr = providerDiagnostic
			case "discovery":
				p.repositoriesErr = providerDiagnostic
			case "verify":
				p.verifyErr = providerDiagnostic
			}
			state, _ := store.Load()
			other := state.Connections[0]
			other.ID = "second"
			state.Connections = append(state.Connections, other)
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "sensitive") || strings.Contains(w.Body.String(), "access_token") {
				t.Fatal("provider diagnostic leaked")
			}
			if scenario != "permissions" && (p.calls != 1 || !p.sawDeadline) {
				t.Fatal("discovery not bounded/batched", p.calls, p.sawDeadline)
			}
			if scenario != "batch" {
				var response struct{ Connections []publisherConnectionView }
				_ = json.Unmarshal(w.Body.Bytes(), &response)
				for _, c := range response.Connections {
					if c.Reconciliation == nil || c.Reconciliation.State != "unavailable" {
						t.Fatalf("provider failure reported healthy %s", w.Body.String())
					}
				}
			}
		})
	}
}

func TestPublisherReconciliationRejectsStaleOrUnsafeReviews(t *testing.T) {
	for _, scenario := range []string{"deletion", "revoked", "disconnected", "app", "key", "branch", "binding", "owner", "permission", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			s, keys, store, base, cookie := publisherServer(t)
			c := connectionForTest(t, s, store, cookie)
			history := realPublisherReceiver(t, s)
			p := &repositoryDiscoveryProvider{publisherProviderFake: base}
			s.publishers.provider = p
			base.repositories[0].FullName = "acme/renamed"
			path := "/api/publishers/" + c.ID + "/reconciliation"
			w := publisherRequest(s, "POST", path+"/plan", map[string]any{}, cookie, "")
			var plan publisherRepositoryReview
			_ = json.Unmarshal(w.Body.Bytes(), &plan)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			state, _ := store.Load()
			want := 409
			switch scenario {
			case "deletion":
				state.Connections[0].Deletion = &publishers.Deletion{}
			case "revoked":
				if err := keys.RevokeKey(c.KeyID); err != nil {
					t.Fatal(err)
				}
			case "disconnected":
				state.Connections[0].State = "disconnected"
			case "app":
				state.App.PEM = "different-key"
			case "key":
				state.Connections[0].PreviousKeyID = "changed"
			case "branch":
				base.repositories[0].DefaultBranch = "next"
			case "binding":
				binding, _ := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
				if err := history.ReconcilePublicationRepository(context.Background(), binding, 12, 7, "acme/renamed"); err != nil {
					t.Fatal(err)
				}
			case "owner":
				base.repositories[0].FullName = "other/renamed"
				want = 403
			case "permission":
				p.verifyErr = errors.New("sensitive detail")
				want = 503
			case "unavailable":
				p.repositoriesErr = errors.New("sensitive detail")
				want = 503
			}
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			w = publisherRequest(s, "POST", path, map[string]any{"fingerprint": plan.Fingerprint, "approved": true}, cookie, "")
			if w.Code != want {
				t.Fatalf("unsafe approval %d want %d: %s", w.Code, want, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "sensitive") {
				t.Fatal("provider detail leaked")
			}
		})
	}
}
