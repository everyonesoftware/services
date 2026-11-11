package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"services/internal/githubapp"
	"services/internal/publishers"
	"testing"
)

type federationFake struct {
	*publisherProviderFake
	subject       string
	subjectErr    error
	permissionErr error
	calls         int
}

func (f *federationFake) FederationSubject(_ context.Context, _ githubapp.App, r githubapp.Repository) (string, error) {
	f.calls++
	return f.subject, f.subjectErr
}
func (f *federationFake) VerifyRepositoryPermissions(context.Context, githubapp.App, githubapp.Repository) error {
	return f.permissionErr
}
func TestPublisherGuidanceWithoutSavedTailnet(t *testing.T) {
	s, _, store, p, cookie := publisherServer(t)
	state, _ := store.Load()
	state.Tailnet = publishers.Tailnet{}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	f := &federationFake{publisherProviderFake: p, subject: "repo:acme@123/app@456:ref:refs/heads/main"}
	s.publishers.provider = f
	body := map[string]any{"repository": p.repositories[0]}
	w := publisherRequest(s, "POST", "/api/publishers/tailnet/guidance", body, cookie, "")
	var got struct {
		Issuer, Subject, SubjectError string
		Tailnet                       publishers.Tailnet
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Subject != f.subject || got.Issuer != "https://token.actions.githubusercontent.com" {
		t.Fatalf("guidance: %d %s", w.Code, w.Body.String())
	}
	f.subject = ""
	f.subjectErr = errors.New("Custom claims unsupported; inspect repository OIDC settings")
	w = publisherRequest(s, "POST", "/api/publishers/tailnet/guidance", body, cookie, "")
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got.Subject != "" || got.SubjectError == "" {
		t.Fatalf("custom fail closed: %d %s", w.Code, w.Body.String())
	}
	f.permissionErr = errors.New("installation permission denied")
	calls := f.calls
	for _, path := range []string{"/api/publishers/tailnet/guidance", "/api/publishers/tailnet"} {
		method := "POST"
		b := body
		if path == "/api/publishers/tailnet" {
			method = "PUT"
			b = map[string]any{"repository": p.repositories[0], "clientId": "new", "audience": "new", "tags": "tag:new"}
		}
		w = publisherRequest(s, method, path, b, cookie, "")
		if w.Code != 403 {
			t.Fatalf("permission bypass %s: %d", path, w.Code)
		}
	}
	if f.calls != calls {
		t.Fatal("subject fetched before access verification")
	}
	forged := p.repositories[0]
	forged.InstallationID++
	w = publisherRequest(s, "POST", "/api/publishers/tailnet/guidance", map[string]any{"repository": forged}, cookie, "")
	if w.Code != 403 {
		t.Fatalf("forged repository: %d", w.Code)
	}
}
