package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"services/internal/auth"
	"services/internal/groups"
	"services/internal/publishers"
	"services/internal/updates"
)

func reviewPublication(t *testing.T) groups.ImagePublication {
	t.Helper()
	var p groups.ImagePublication
	if err := json.Unmarshal([]byte(publicationBody(t)), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublisherConfigurationFailureFencesManagedIngestion(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	// Simulate restart with the same authentication store, but failed publisher startup.
	s.publishers = nil
	if err := s.ConfigurePublishers(nil, nil); err == nil {
		t.Fatal("expected configuration failure")
	}
	p := reviewPublication(t)
	p.SourceRepository = "other/app"
	p.RunURL = "https://github.com/other/app/actions/runs/123"
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret); w.Code != 503 {
		t.Fatalf("failed startup bypassed binding: %d %s", w.Code, w.Body.String())
	}
}

func TestPublisherFailureFencePreservesManualAndLegacyModes(t *testing.T) {
	s, keys := securedServer(t)
	adminSession(t, s)
	r := &publicationReceiver{}
	if err := s.ConfigurePublicationNotifications(notificationToken, []string{"ghcr.io/acme/app"}, r); err != nil {
		t.Fatal(err)
	}
	_, token, err := keys.CreateKey("manual", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, "")
	if err != nil {
		t.Fatal(err)
	}
	p := reviewPublication(t)
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, token); w.Code != 200 {
		t.Fatalf("manual key without integration: %d %s", w.Code, w.Body.String())
	}
	if err := s.ConfigurePublishers(nil, nil); err == nil {
		t.Fatal("expected startup failure")
	}
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, token); w.Code != 503 {
		t.Fatalf("missing metadata must fence keys: %d", w.Code)
	}
	if w := publisherRequest(s, "POST", "/api/images/published", p, nil, notificationToken); w.Code != 200 {
		t.Fatalf("unrelated legacy token fenced: %d %s", w.Code, w.Body.String())
	}
	if r.calls != 2 {
		t.Fatalf("receiver calls: %d", r.calls)
	}
}

func TestPublisherDuplicateReconcilesReceiptAfterSaveFailure(t *testing.T) {
	s, _, store, _, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	directory := filepath.Join(t.TempDir(), "receipt-store")
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	store, err = publishers.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	s.publishers.store = store
	receiver, err := updates.OpenPublicationStore(filepath.Join(t.TempDir(), "publications.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	// The checker is the production receiver over real persistent publication storage.
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(receiver)
	if err := s.ConfigurePublicationReceiver(checker); err != nil {
		t.Fatal(err)
	}
	p := reviewPublication(t)
	p.PublishedAt = "2026-11-09T12:00:00Z"
	send := func(want int) {
		t.Helper()
		if w := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret); w.Code != want {
			t.Fatalf("publication: %d %s", w.Code, w.Body.String())
		}
	}
	send(200)
	db, err := sql.Open("sqlite", filepath.Join(directory, "publishers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER fail_receipt BEFORE UPDATE ON publisher_state BEGIN SELECT RAISE(FAIL,'receipt unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	p.RunID = "124"
	p.RunURL = "https://github.com/acme/app/actions/runs/124"
	p.PublishedAt = "2026-11-09T13:00:00Z"
	p.Version = "new"
	send(503)
	state, _ = store.Load()
	if state.Connections[0].LastPublication.Version == "new" {
		t.Fatal("failed save changed receipt")
	}
	if _, err = db.Exec("DROP TRIGGER fail_receipt"); err != nil {
		t.Fatal(err)
	}
	send(200)
	state, _ = store.Load()
	if state.Connections[0].LastPublication.Version != "new" {
		t.Fatal("duplicate retry left prior receipt forever")
	}
	// Ordering metadata must survive a genuine publisher store reopen.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = publishers.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s.publishers.store = store
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	send(200)
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Connections[0].LastPublication.ReceivedAt != before.Connections[0].LastPublication.ReceivedAt || state.Connections[0].LastPublicationRunID != p.RunID || state.Connections[0].LastPublicationRunAttempt != p.RunAttempt {
		t.Fatal("duplicate changed receipt or ordering metadata did not persist")
	}
	previous := p
	p.RunID = "125"
	p.RunURL = "https://github.com/acme/app/actions/runs/125"
	p.Version = "newest"
	send(200)
	p = previous
	send(200)
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Connections[0].LastPublication.Version != "newest" {
		t.Fatal("older duplicate overwrote newer saved receipt")
	}
}

func TestPublisherLegacyReceiptNeverRegressesUnknownAttempt(t *testing.T) {
	s, keys, store, _, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	p := reviewPublication(t)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	state.Connections[0].LastPublication = &publishers.PublicationReceipt{PublishedAt: p.PublishedAt, RunURL: p.RunURL, Digest: p.Digest, Commit: p.Commit, Version: "newer legacy attempt", ReceivedAt: "original receipt time"}
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	key, err := keys.AuthenticateKey(c.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.recordPublisherReceipt(key, p, "duplicate"); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Connections[0].LastPublication.Version != "newer legacy attempt" {
		t.Fatal("unknown legacy attempt was regressed by replay")
	}
	p.RunID = "124"
	p.RunURL = "https://github.com/acme/app/actions/runs/124"
	if err = s.recordPublisherReceipt(key, p, "accepted"); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Connections[0].LastPublication.RunURL != p.RunURL {
		t.Fatal("new numeric run could not advance legacy receipt")
	}
}

type pausedPublicationReceiver struct{ entered, release chan struct{} }

func (p *pausedPublicationReceiver) AcceptPublication(_ context.Context, event groups.ImagePublication) (string, error) {
	if event.Version == "older" {
		close(p.entered)
		<-p.release
	}
	return "accepted", nil
}
func TestPublisherConcurrentReceiptsCannotRegress(t *testing.T) {
	for _, tc := range []struct {
		name, oldRun, newRun   string
		oldAttempt, newAttempt int
		oldTime, newTime       string
	}{
		{"timestamp", "124", "123", 1, 1, "2026-11-09T12:00:00Z", "2026-11-09T13:00:00Z"},
		{"numeric run", "99", "100", 1, 1, "2026-11-09T12:00:00Z", "2026-11-09T12:00:00Z"},
		{"attempt", "123", "123", 1, 2, "2026-11-09T12:00:00Z", "2026-11-09T12:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, store, _, cookie := publisherServer(t)
			c := connectionForTest(t, s, store, cookie)
			r := &pausedPublicationReceiver{make(chan struct{}), make(chan struct{})}
			if err := s.ConfigurePublicationReceiver(r); err != nil {
				t.Fatal(err)
			}
			old := reviewPublication(t)
			old.Version = "older"
			old.RunID = tc.oldRun
			old.RunAttempt = tc.oldAttempt
			old.RunURL = "https://github.com/acme/app/actions/runs/" + old.RunID
			old.PublishedAt = tc.oldTime
			next := old
			next.Version = "newer"
			next.RunID = tc.newRun
			next.RunAttempt = tc.newAttempt
			next.RunURL = "https://github.com/acme/app/actions/runs/" + next.RunID
			next.PublishedAt = tc.newTime
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- publisherRequest(s, "POST", "/api/images/published", old, nil, c.Secret) }()
			<-r.entered
			w := publisherRequest(s, "POST", "/api/images/published", next, nil, c.Secret)
			close(r.release)
			a := <-done
			if w.Code != 200 || a.Code != 200 {
				t.Fatalf("requests: %d %d", w.Code, a.Code)
			}
			state, _ := store.Load()
			if state.Connections[0].LastPublication.Version != "newer" {
				t.Fatal("late older receipt replaced newer event")
			}
		})
	}
}

func TestPublisherRotationReplacesRevokedPendingKey(t *testing.T) {
	s, keys, store, base, cookie := publisherServer(t)
	p := &lifecycleProvider{publisherProviderFake: base}
	s.publishers.provider = p
	c := connectionForTest(t, s, store, cookie)
	path := "/api/publishers/" + c.ID + "/rotate"
	if w := publisherRequest(s, "POST", path, map[string]any{}, cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	state, _ := store.Load()
	first := state.Connections[0]
	challenge := base.challenge
	if err := keys.RevokeKey(first.KeyID); err != nil {
		t.Fatal(err)
	}
	w := publisherRequest(s, "POST", path, map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	state, _ = store.Load()
	next := state.Connections[0]
	if next.KeyID == first.KeyID || next.PreviousKeyID != c.KeyID || next.PreviousSecret != c.Secret || base.challenge == challenge {
		t.Fatal("revoked pending replacement was reused or original lost")
	}
	if strings.Contains(w.Body.String(), next.Secret) {
		t.Fatal("secret leaked")
	}
	if w = publisherRequest(s, "POST", "/api/publishers/callback", callbackBody(next, base.challenge), nil, next.Secret); w.Code != 200 {
		t.Fatalf("replacement verification: %d %s", w.Code, w.Body.String())
	}
	list, _ := keys.ListKeys()
	if len(list) != 1 || list[0].ID != next.KeyID {
		t.Fatal("rotation leaked keys")
	}
}

func TestPublisherDisconnectRetriesRemoteDisable(t *testing.T) {
	s, _, store, base, cookie := publisherServer(t)
	p := &lifecycleProvider{publisherProviderFake: base}
	s.publishers.provider = p
	c := connectionForTest(t, s, store, cookie)
	path := "/api/publishers/" + c.ID + "/disconnect"
	base.enableErr = errors.New("offline")
	if w := publisherRequest(s, "POST", path, map[string]any{}, cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"remoteDisablePending":true`) {
		t.Errorf("pending remote cleanup not exposed: %d %s", w.Code, w.Body.String())
	}
	pending, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !pending.Connections[0].RemoteDisablePending || pending.Connections[0].Configured {
		t.Fatal("remote pending state was not persisted separately from configuration")
	}
	if w := publisherRequest(s, "GET", "/api/publishers", nil, cookie, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"remoteDisablePending":true`) {
		t.Fatalf("status omitted cleanup retry: %s", w.Body.String())
	}
	base.enableErr = nil
	w := publisherRequest(s, "POST", path, map[string]any{}, cookie, "")
	if w.Code != 200 || p.disabled != 2 || strings.Contains(w.Body.String(), `"remoteDisablePending":true`) {
		t.Fatalf("remote cleanup not retried: calls=%d %s", p.disabled, w.Body.String())
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.Connections[0].RemoteDisablePending || state.Connections[0].Error != "" {
		t.Fatal("successful readback did not clear pending status")
	}
}

func TestPublisherRotationPendingKeyExpiryAndStorageError(t *testing.T) {
	for _, failure := range []string{"expiry", "storage"} {
		t.Run(failure, func(t *testing.T) {
			s, _, store, base, _ := publisherServer(t)
			path := filepath.Join(t.TempDir(), "auth.db")
			keys, err := auth.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer keys.Close()
			s.authStore = keys
			origin := s.publicOrigin
			s.publicOrigin = nil
			cookie := adminSession(t, s)
			s.publicOrigin = origin
			p := &lifecycleProvider{publisherProviderFake: base}
			s.publishers.provider = p
			c := connectionForTest(t, s, store, cookie)
			route := "/api/publishers/" + c.ID + "/rotate"
			if w := publisherRequest(s, "POST", route, map[string]any{}, cookie, ""); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			state, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			first := state.Connections[0]
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if failure == "expiry" {
				_, err = db.Exec("UPDATE api_keys SET expires_at=? WHERE id=?", time.Now().Add(-time.Minute).Unix(), first.KeyID)
			} else {
				_, err = db.Exec(`CREATE TRIGGER fail_key_status BEFORE UPDATE ON api_keys BEGIN SELECT RAISE(FAIL,'auth storage unavailable'); END`)
			}
			if err != nil {
				t.Fatal(err)
			}
			w := publisherRequest(s, "POST", route, map[string]any{}, cookie, "")
			state, err = store.Load()
			if err != nil {
				t.Fatal(err)
			}
			next := state.Connections[0]
			list, err := keys.ListKeys()
			if err != nil {
				t.Fatal(err)
			}
			if failure == "storage" {
				if w.Code != 503 || next.KeyID != first.KeyID || len(list) != 2 || p.rotated != 1 {
					t.Fatalf("database failure treated as revoked: %d keys=%d deliveries=%d", w.Code, len(list), p.rotated)
				}
			} else {
				if w.Code != 200 || next.KeyID == first.KeyID || next.PreviousKeyID != c.KeyID || len(list) != 2 {
					t.Fatalf("expired pending key not replaced cleanly: %d keys=%d", w.Code, len(list))
				}
			}
		})
	}
}
