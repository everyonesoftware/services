package dashboard

import (
	"errors"
	"net/http"

	"strings"
	"testing"

	"services/internal/githubapp"
	"services/internal/publishers"
)

func deletionTestServer(t *testing.T) (*Server, *publishers.Store, *deletionProvider, *http.Cookie, publishers.Connection) {
	t.Helper()
	s, _, store, base, cookie := publisherServer(t)
	p := &deletionProvider{lifecycleProvider: &lifecycleProvider{publisherProviderFake: base}, removal: githubapp.WorkflowPlan{Supported: true, Problems: []string{}, Files: []githubapp.WorkflowFile{}, Fingerprint: strings.Repeat("c", 64)}}
	s.publishers.provider = p
	return s, store, p, cookie, connectionForTest(t, s, store, cookie)
}
func TestPublisherDeletionCannotBypassManualRevocationOrFreshWorkflowCheck(t *testing.T) {
	s, store, p, cookie, c := deletionTestServer(t)
	path := "/api/publishers/" + c.ID
	if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 409 {
		t.Fatalf("deletion without manual revocation accepted: %d %s", w.Code, w.Body.String())
	}
	if w := publisherRequest(s, "POST", path+"/deletion/tailscale", deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated"), cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p.removal.Files = []githubapp.WorkflowFile{{Path: ".github/workflows/publish.yml", Before: "notifications restored", After: "build"}}
	if w := publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 409 {
		t.Fatalf("stale workflow proof accepted: %d %s", w.Code, w.Body.String())
	}
	state, _ := store.Load()
	if len(state.Connections) != 1 {
		t.Fatal("blocked removal lost connection")
	}
	p.removal.Files = []githubapp.WorkflowFile{}
	p.removalErr = errors.New("offline")
	if w := publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 409 {
		t.Fatalf("network error bypassed: %d", w.Code)
	}
	p.removalErr = nil
	if w := publisherRequest(s, "DELETE", path, nil, cookie, ""); w.Code != 200 {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
}

func TestPublisherDeletionBlocksReconnectAfterTailscaleRevocation(t *testing.T) {
	s, _, _, cookie, c := deletionTestServer(t)
	path := "/api/publishers/" + c.ID
	if w := publisherRequest(s, "POST", path+"/deletion/tailscale", deletionAttestationFromReview(t, s, c.ID, cookie, "dedicated"), cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "POST", path+"/test", map[string]any{}, cookie, ""); w.Code != 409 {
		t.Fatalf("test dispatch after revocation accepted: %d %s", w.Code, w.Body.String())
	}
	if w := publisherRequest(s, "POST", path+"/disconnect", map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "POST", path+"/reconnect", map[string]any{}, cookie, ""); w.Code != 409 {
		t.Fatalf("reconnect during deletion accepted: %d %s", w.Code, w.Body.String())
	}
}
