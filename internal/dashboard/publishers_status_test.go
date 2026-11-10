package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublisherStatusStopsClaimingVerifiedWhenKeyRevoked(t *testing.T) {
	s, keys, store, provider, cookie := publisherServer(t)
	w := publisherRequest(s, "POST", "/api/publishers", publisherSetupBody(), cookie, "")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	state, _ := store.Load()
	c := state.Connections[0]
	body := map[string]string{"connectionId": c.ID, "challenge": provider.challenge, "sourceRepository": "acme/app", "ref": "refs/heads/main", "runId": "123"}
	if w = publisherRequest(s, "POST", "/api/publishers/callback", body, nil, c.Secret); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := keys.RevokeKey(c.KeyID); err != nil {
		t.Fatal(err)
	}
	w = publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var status struct {
		Connections []publisherConnectionView `json:"connections"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Connections[0].State != "error" || !strings.Contains(status.Connections[0].Error, "revoked") {
		t.Fatal("revoked integration still presented as verified")
	}
}
