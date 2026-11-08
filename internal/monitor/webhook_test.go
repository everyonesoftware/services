package monitor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"services/internal/groups"
	"strings"
	"testing"
)

func TestWebhookRetriesAndDeduplicatesWithoutInspectionSecrets(t *testing.T) {
	calls := 0
	var received []Event
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("bad request %s", r.Method)
		}
		if strings.Contains(string(body), "SECRET") {
			t.Error("secret sent")
		}
		if calls == 1 {
			http.Error(w, "retry", 500)
			return
		}
		var event Event
		if err := json.Unmarshal(body, &event); err != nil {
			t.Error(err)
		}
		received = append(received, event)
		w.WriteHeader(204)
	}))
	defer server.Close()
	if NewWebhook("") != nil {
		t.Fatal("empty webhook must disable notifications")
	}
	s := &source{containers: []groups.Container{{Name: "web", State: "running", Health: "unhealthy", HealthOutput: "SECRET", ComposeWorkingDir: "SECRET", Image: "SECRET"}}}
	m := New(s, NewWebhook(server.URL))
	m.Poll(context.Background())
	// Snapshot must remain readable even when notification delivery occurs.
	if !m.Snapshot().NotificationConfigured {
		t.Fatal("not configured")
	}
	m.Poll(context.Background())
	m.Poll(context.Background())
	if calls != 2 || len(received) != 1 || received[0].Kind != "problem" {
		t.Fatalf("calls %d events %+v", calls, received)
	}
	s.containers[0].Health = "healthy"
	m.Poll(context.Background())
	m.Poll(context.Background())
	if calls != 3 || len(received) != 2 || received[1].Kind != "recovery" {
		t.Fatalf("calls %d events %+v", calls, received)
	}
}
func TestWebhookRejectsRedirectAndSanitizesFailure(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++ }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	err := NewWebhook(redirect.URL).Notify(context.Background(), Event{Kind: "problem"})
	if err == nil || targetCalls != 0 {
		t.Fatalf("redirect followed: %d %v", targetCalls, err)
	}
	err = NewWebhook("file:///SECRET").Notify(context.Background(), Event{})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("unsafe error %v", err)
	}
}
