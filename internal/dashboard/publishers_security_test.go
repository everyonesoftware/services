package dashboard

import (
	"encoding/json"

	"net/url"
	"strings"
	"testing"

	"services/internal/githubapp"
	"services/internal/publishers"
)

func TestPublisherOrganizationAppRegistrationAndStateReplay(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	state.App = publishers.App{}
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	w := publisherRequest(s, "POST", "/api/publishers/github/start", map[string]string{"organization": "everyonesoftware"}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("organization registration=%d %s", w.Code, w.Body.String())
	}
	var start struct {
		Action   string `json:"action"`
		Manifest string `json:"manifest"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(start.Action)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "github.com" || u.Path != "/organizations/everyonesoftware/settings/apps/new" {
		t.Fatalf("wrong registration target: %s", u.Path)
	}
	body := map[string]string{"state": u.Query().Get("state"), "code": "0123456789abcdef0123456789abcdef01234567"}
	w = publisherRequest(s, "POST", "/api/publishers/github/complete", body, cookie, "")
	if w.Code != 200 {
		t.Fatalf("complete=%d %s", w.Code, w.Body.String())
	}
	w = publisherRequest(s, "POST", "/api/publishers/github/complete", body, cookie, "")
	if w.Code != 403 {
		t.Fatalf("replayed registration=%d", w.Code)
	}
	w = publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	if strings.Contains(w.Body.String(), "private-app-key") || !strings.Contains(w.Body.String(), "installations/new") {
		t.Fatal("unsafe or incomplete app status")
	}
}
func TestPublisherRejectsOrganizationPathInjection(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	state, _ := store.Load()
	state.App = publishers.App{}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	for _, org := range []string{"../other", "org?state=wrong", "org/name", " bad", ""} {
		if org == "" {
			continue
		}
		w := publisherRequest(s, "POST", "/api/publishers/github/start", map[string]string{"organization": org}, cookie, "")
		if w.Code != 400 {
			t.Fatalf("organization injection accepted: status %d", w.Code)
		}
	}
}
func TestPublisherProvisioningFailureRevokesCreatedKey(t *testing.T) {
	s, keys, store, provider, cookie := publisherServer(t)
	provider.configureErr = githubapp.ErrConflict
	w := publisherRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, "")
	if w.Code != 409 {
		t.Fatalf("failed setup=%d", w.Code)
	}
	list, err := keys.ListKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatal("failed provisioning left active key")
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := state.Connections[0]
	if c.State != "error" || c.Secret != "" || provider.dispatched != 0 {
		t.Fatal("failed provisioning pretended to dispatch")
	}
	w = publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/test", map[string]any{}, cookie, "")
	if w.Code != 409 {
		t.Fatalf("incomplete config retried unsafely=%d", w.Code)
	}
}
func TestPublisherTestRetryInvalidatesOldChallengeWithoutAnotherKey(t *testing.T) {
	s, keys, store, provider, cookie := publisherServer(t)
	w := publisherRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, "")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	oldChallenge := provider.challenge
	state, _ := store.Load()
	c := state.Connections[0]
	w = publisherRequest(s, "POST", "/api/publishers/"+c.ID+"/test", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatalf("retry=%d %s", w.Code, w.Body.String())
	}
	if provider.challenge == oldChallenge {
		t.Fatal("challenge reused on explicit retry")
	}
	list, _ := keys.ListKeys()
	if len(list) != 1 {
		t.Fatal("retry created another key")
	}
	body := map[string]string{"connectionId": c.ID, "challenge": oldChallenge, "sourceRepository": "acme/app", "ref": "refs/heads/main", "runId": "123"}
	w = publisherRequest(s, "POST", "/api/publishers/callback", body, nil, c.Secret)
	if w.Code != 403 {
		t.Fatalf("old challenge accepted=%d", w.Code)
	}
}
