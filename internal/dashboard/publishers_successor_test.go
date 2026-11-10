package dashboard

import (
	"errors"
	"testing"
)

func TestPublisherObsoleteCleanupDoesNotDisableSuccessor(t *testing.T) {
	s, _, store, base, cookie := publisherServer(t)
	p := &lifecycleProvider{publisherProviderFake: base}
	s.publishers.provider = p
	old := connectionForTest(t, s, store, cookie)
	base.enableErr = errors.New("GitHub unavailable")
	path := "/api/publishers/" + old.ID + "/disconnect"
	if w := publisherRequest(s, "POST", path, map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	base.enableErr = nil
	// Repository conflicts were explicitly resolved before connecting a successor.
	if w := publisherRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, ""); w.Code != 201 {
		t.Fatalf("successor: %d %s", w.Code, w.Body.String())
	}
	state, _ := store.Load()
	next := state.Connections[1]
	if w := publisherRequest(s, "POST", "/api/publishers/callback", callbackBody(next, base.challenge), nil, next.Secret); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := publisherRequest(s, "POST", path, map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if p.disabled != 1 {
		t.Fatal("obsolete cleanup disabled successor repository notifications")
	}
	state, _ = store.Load()
	if state.Connections[0].RemoteDisablePending || state.Connections[1].State != "verified" {
		t.Fatal("obsolete cleanup was not reconciled independently of the successor")
	}
}
