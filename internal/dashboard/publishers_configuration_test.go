package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"services/internal/githubapp"
)

type setupConfigurationProvider struct {
	*repairProvider
	inspected  int
	planErr    error
	beforePlan func()
}

func (p *setupConfigurationProvider) PlanConfiguration(ctx context.Context, app githubapp.App, repo githubapp.Repository, variables map[string]string) (githubapp.ConfigurationPlan, error) {
	p.inspected++
	if p.beforePlan != nil {
		p.beforePlan()
	}
	if p.planErr != nil {
		return githubapp.ConfigurationPlan{}, p.planErr
	}
	return p.repairProvider.PlanConfiguration(ctx, app, repo, variables)
}

func setupConfigurationFake(p *publisherProviderFake) *setupConfigurationProvider {
	return &setupConfigurationProvider{repairProvider: &repairProvider{publisherProviderFake: p, plan: githubapp.ConfigurationPlan{
		SecretExists: true, Fingerprint: strings.Repeat("a", 64),
		Variables: []githubapp.ConfigurationVariable{
			{Name: "SERVICES_PUBLISH_URL", Exists: true, CurrentValue: "https://old.example/api/images/published", DesiredValue: "https://localhost/api/images/published"},
			{Name: "SERVICES_PUBLISH_NOTIFICATIONS", Exists: true, CurrentValue: "true", DesiredValue: "false"},
			{Name: "TAILSCALE_CLIENT_ID", Exists: true, CurrentValue: "Old-Client", DesiredValue: "client-id"},
			{Name: "TAILSCALE_AUDIENCE", Exists: true, CurrentValue: "old-audience", DesiredValue: "audience"},
			{Name: "TAILSCALE_TAGS", Exists: true, CurrentValue: "tag:old", DesiredValue: "tag:publisher"},
		},
	}}}
}

func TestPublisherSetupPlanInspectsConfigurationWithoutMutations(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := setupConfigurationFake(base)
	s.publishers.provider = p
	// Permission verification metadata is established separately from review.
	if w := publisherRequest(s, "POST", "/api/publishers/github/permissions", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatalf("permission check=%d %s", w.Code, w.Body.String())
	}
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	w := publisherRequest(s, "POST", "/api/publishers/plan", publisherSetupBody(), cookie, "")
	if w.Code != 200 {
		t.Fatalf("plan=%d %s", w.Code, w.Body.String())
	}
	var response struct {
		Configuration githubapp.ConfigurationPlan `json:"configuration"`
		Approval      string                      `json:"approval"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if p.inspected != 1 || !reflect.DeepEqual(response.Configuration, p.plan) {
		t.Fatalf("configuration not reviewed: inspections=%d configuration=%+v", p.inspected, response.Configuration)
	}
	if response.Approval == publisherSetupBody()["approval"] {
		t.Fatal("approval does not bind configuration snapshot")
	}
	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	list, err := keys.ListKeys()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || len(list) != 0 || p.configured != 0 || p.applied != 0 || p.dispatched != 0 {
		t.Fatal("read-only review mutated local or remote state")
	}
}

func TestPublisherSetupConfigurationInspectionFailureBlocksReview(t *testing.T) {
	s, _, _, base, cookie := publisherServer(t)
	p := setupConfigurationFake(base)
	p.planErr = errors.New("provider unavailable")
	s.publishers.provider = p
	w := publisherRequest(s, "POST", "/api/publishers/plan", publisherSetupBody(), cookie, "")
	if w.Code != 502 || !strings.Contains(w.Body.String(), "configuration") {
		t.Fatalf("plan=%d %s", w.Code, w.Body.String())
	}
}

func reviewedSetup(t *testing.T, s *Server, cookie *http.Cookie) map[string]any {
	t.Helper()
	body := publisherSetupBody()
	w := publisherRequest(s, "POST", "/api/publishers/plan", body, cookie, "")
	if w.Code != 200 {
		t.Fatalf("plan=%d %s", w.Code, w.Body.String())
	}
	var response struct {
		Approval      string                      `json:"approval"`
		Configuration githubapp.ConfigurationPlan `json:"configuration"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	body["approval"] = response.Approval
	body["configurationFingerprint"] = response.Configuration.Fingerprint
	return body
}

func TestPublisherSetupApprovedConfigurationReconciliation(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := setupConfigurationFake(base)
	p.configureErr = githubapp.ErrConflict
	s.publishers.provider = p
	body := reviewedSetup(t, s, cookie)
	p.beforePlan = func() {
		state, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		list, err := keys.ListKeys()
		if err != nil {
			t.Fatal(err)
		}
		if len(state.Connections) != 0 || len(list) != 0 {
			t.Fatal("inspection happened after local provisioning")
		}
	}
	p.beforeApply = func() {
		state, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(state.Connections) != 1 || state.Connections[0].Secret == "" || state.Connections[0].Configured {
			t.Fatal("credential intent not persisted before remote writes")
		}
	}
	w := publisherRequest(s, "POST", "/api/publishers", body, cookie, "")
	if w.Code != 201 {
		t.Fatalf("setup=%d %s", w.Code, w.Body.String())
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := state.Connections[0]
	if p.inspected != 2 || p.applied != 1 || p.configured != 0 || p.dispatched != 1 || p.enabled != 0 || !c.Configured || c.State != "awaiting_callback" || p.delivered != c.Secret {
		t.Fatalf("unverified setup: %+v", connectionView(c))
	}
	if strings.Contains(w.Body.String(), c.Secret) {
		t.Fatal("secret leaked")
	}
}

func TestPublisherSetupRejectsUnreviewedConfigurationBeforeMutations(t *testing.T) {
	for _, mode := range []string{"snapshot-changed", "fingerprint-mismatch", "approval-missing", "legacy-approval", "inspection-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			s, keys, store, base, cookie := publisherServer(t)
			p := setupConfigurationFake(base)
			s.publishers.provider = p
			body := reviewedSetup(t, s, cookie)
			switch mode {
			case "snapshot-changed":
				p.plan.Fingerprint = strings.Repeat("b", 64)
			case "fingerprint-mismatch":
				body["configurationFingerprint"] = strings.Repeat("b", 64)
			case "approval-missing":
				delete(body, "approval")
			case "legacy-approval":
				body["approval"] = publisherSetupBody()["approval"]
			case "inspection-unavailable":
				p.planErr = errors.New("provider unavailable")
			}
			before, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			w := publisherRequest(s, "POST", "/api/publishers", body, cookie, "")
			want := 409
			if mode == "inspection-unavailable" {
				want = 502
			}
			if w.Code != want {
				t.Fatalf("setup=%d %s", w.Code, w.Body.String())
			}
			after, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			list, err := keys.ListKeys()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) || len(list) != 0 || p.configured != 0 || p.applied != 0 || p.dispatched != 0 {
				t.Fatal("rejected approval mutated local or remote state")
			}
		})
	}
}

func TestPublisherSetupWithoutExplicitConfigurationApprovalNeverReconciles(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := setupConfigurationFake(base)
	p.configureErr = githubapp.ErrConflict
	s.publishers.provider = p
	body := reviewedSetup(t, s, cookie)
	delete(body, "configurationFingerprint")
	w := publisherRequest(s, "POST", "/api/publishers", body, cookie, "")
	if w.Code != 409 || p.applied != 0 || p.configured != 1 || p.dispatched != 0 {
		t.Fatalf("implicit overwrite: status=%d configured=%d repaired=%d", w.Code, p.configured, p.applied)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	list, err := keys.ListKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 || len(state.Connections) != 1 || state.Connections[0].State != "error" || state.Connections[0].Secret != "" {
		t.Fatal("conflict did not retain safe repair state")
	}
}

func TestPublisherSetupReconciliationFailureRetainsCredentialAndReportsPartialWrites(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := setupConfigurationFake(base)
	p.applyErr = errors.New("write readback failed")
	s.publishers.provider = p
	body := reviewedSetup(t, s, cookie)
	w := publisherRequest(s, "POST", "/api/publishers", body, cookie, "")
	if w.Code != 502 || !strings.Contains(w.Body.String(), "may have changed") || !strings.Contains(w.Body.String(), "saved connection") {
		t.Fatalf("uncertain setup=%d %s", w.Code, w.Body.String())
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	list, err := keys.ListKeys()
	if err != nil {
		t.Fatal(err)
	}
	c := state.Connections[0]
	if len(list) != 1 || c.Secret == "" || c.Configured || c.State != "error" || !strings.Contains(c.Error, "may have changed") || p.dispatched != 0 {
		t.Fatalf("unsafe partial failure state: %+v", connectionView(c))
	}
}
