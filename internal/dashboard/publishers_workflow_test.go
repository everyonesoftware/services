package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"services/internal/githubapp"
)

type workflowProviderFake struct {
	*publisherProviderFake
	installed   int
	plan        githubapp.WorkflowPlan
	fingerprint string
}

func (p *workflowProviderFake) PlanWorkflow(context.Context, githubapp.App, githubapp.Repository) (githubapp.WorkflowPlan, error) {
	return p.plan, nil
}
func (p *workflowProviderFake) InstallWorkflow(_ context.Context, _ githubapp.App, _ githubapp.Repository, fingerprint string) (string, error) {
	p.installed++
	p.fingerprint = fingerprint
	return "https://github.com/acme/app/pull/12", nil
}
func TestPublisherWorkflowPlanAndExplicitPRApproval(t *testing.T) {
	s, keys, _, base, cookie := publisherServer(t)
	p := &workflowProviderFake{publisherProviderFake: base, plan: githubapp.WorkflowPlan{Supported: true, Problems: []string{}, Files: []githubapp.WorkflowFile{{Path: "scripts/publisher-connection-test.mjs", After: "callback sender"}}, Fingerprint: strings.Repeat("a", 64)}}
	s.publishers.provider = p
	w := publisherRequest(s, "POST", "/api/publishers/plan", publisherSetupBody(), cookie, "")
	if w.Code != 200 {
		t.Fatalf("plan %d %s", w.Code, w.Body.String())
	}
	var plan struct {
		Workflow *githubapp.WorkflowPlan `json:"workflow"`
		Approval string                  `json:"approval"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Workflow == nil || len(plan.Workflow.Files) != 1 || len(plan.Approval) != 64 {
		t.Fatalf("review incomplete: %s", w.Body.String())
	}
	if p.installed != 0 || base.configured != 0 {
		t.Fatal("planning mutated GitHub")
	}
	body := publisherSetupBody()
	body["fingerprint"] = p.plan.Fingerprint
	delete(body, "approval")
	if w = publisherRequest(s, "POST", "/api/publishers/workflow", body, cookie, ""); w.Code != 201 {
		t.Fatalf("PR: %d %s", w.Code, w.Body.String())
	}
	if p.installed != 1 || p.fingerprint != p.plan.Fingerprint || !strings.Contains(w.Body.String(), "/pull/12") {
		t.Fatal("wrong PR approval")
	}
	list, _ := keys.ListKeys()
	if len(list) != 0 {
		t.Fatal("PR setup prematurely created publisher key")
	}
}
func TestPublisherRejectsStaleConfigurationApproval(t *testing.T) {
	s, _, store, provider, cookie := publisherServer(t)
	w := publisherRequest(s, "POST", "/api/publishers/plan", publisherSetupBody(), cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var plan struct {
		Approval string `json:"approval"`
	}
	json.Unmarshal(w.Body.Bytes(), &plan)
	state, _ := store.Load()
	tailnet := state.Tailnets["12"]
	tailnet.Audience = "changed-audience"
	state.Tailnets["12"] = tailnet
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	body := publisherSetupBody()
	body["approval"] = plan.Approval
	if w = publisherRequest(s, "POST", "/api/publishers", body, cookie, ""); w.Code != 409 {
		t.Fatalf("stale approval: %d %s", w.Code, w.Body.String())
	}
	if provider.configured != 0 {
		t.Fatal("stale review changed GitHub")
	}
}
func TestPublisherWorkflowInstallRejectsBearerAndCrossOrigin(t *testing.T) {
	s, keys, _, base, cookie := publisherServer(t)
	p := &workflowProviderFake{publisherProviderFake: base}
	s.publishers.provider = p
	_, token, _ := keys.CreateKey("publisher", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
	if w := publisherRequest(s, "POST", "/api/publishers/workflow", map[string]string{}, nil, token); w.Code != http.StatusForbidden {
		t.Fatalf("bearer: %d", w.Code)
	}
	r := httptest.NewRequest("POST", "https://localhost/api/publishers/workflow", strings.NewReader(`{}`))
	r.AddCookie(cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://attacker.invalid")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || p.installed != 0 {
		t.Fatalf("cross origin: %d; workflow installs=%d", w.Code, p.installed)
	}
}
