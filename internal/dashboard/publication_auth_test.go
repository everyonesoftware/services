package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"services/internal/groups"
)

type publicationReceiver struct {
	calls  int
	result string
	err    error
}

func (f *publicationReceiver) AcceptPublication(_ context.Context, _ groups.ImagePublication) (string, error) {
	f.calls++
	if f.result == "" {
		f.result = "accepted"
	}
	return f.result, f.err
}

const notificationToken = "test-notification-token-with-at-least-32-characters"

func publicationBody(t *testing.T) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"schemaVersion": 1, "repository": "ghcr.io/acme/app", "tags": []string{"latest", "main"},
		"digest": "sha256:" + strings.Repeat("a", 64), "version": "42", "sourceRepository": "acme/app",
		"commit": strings.Repeat("b", 40), "ref": "refs/heads/main", "runId": "123", "runAttempt": 1,
		"runUrl": "https://github.com/acme/app/actions/runs/123", "publishedAt": time.Now().UTC().Add(-time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
func publicationRequest(t *testing.T, server *Server, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/images/published", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}

func TestPublicationEndpointRequiresScopedCredential(t *testing.T) {
	receiver := &publicationReceiver{}
	server := New(fakeSource{}, 1)
	if err := server.ConfigurePublicationNotifications(notificationToken, []string{"ghcr.io/acme/app"}, receiver); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "wrong", notificationToken + "extra"} {
		response := publicationRequest(t, server, publicationBody(t), token)
		if response.Code != 401 || receiver.calls != 0 {
			t.Fatalf("unauthorized response=%d calls=%d", response.Code, receiver.calls)
		}
	}
	body := strings.Replace(publicationBody(t), `"repository":"ghcr.io/acme/app"`, `"repository":"ghcr.io/acme/other"`, 1)
	response := publicationRequest(t, server, body, notificationToken)
	if response.Code != 403 || receiver.calls != 0 {
		t.Fatalf("out-of-scope response=%d calls=%d body=%s", response.Code, receiver.calls, response.Body.String())
	}
	response = publicationRequest(t, server, publicationBody(t), notificationToken)
	if response.Code != 200 || receiver.calls != 1 || !strings.Contains(response.Body.String(), `"result":"accepted"`) {
		t.Fatalf("authorized response=%d calls=%d body=%s", response.Code, receiver.calls, response.Body.String())
	}
}

func TestPublicationEndpointRejectsInvalidEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		want                    int
	}{
		{"malformed", "{", "application/json", 400}, {"empty", "{}", "application/json", 400},
		{"unknown field", strings.TrimSuffix(publicationBody(t), "}") + `,"unexpected":true}`, "application/json", 400},
		{"trailing JSON", publicationBody(t) + "{}", "application/json", 400},
		{"invalid digest", strings.Replace(publicationBody(t), "sha256:"+strings.Repeat("a", 64), "sha256:bad", 1), "application/json", 400},
		{"large", strings.Repeat(" ", 65537), "application/json", 413}, {"wrong media", publicationBody(t), "text/plain", 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &publicationReceiver{}
			server := New(fakeSource{}, 1)
			if err := server.ConfigurePublicationNotifications(notificationToken, []string{"ghcr.io/acme/app"}, receiver); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("POST", "http://localhost/api/images/published", strings.NewReader(tc.body))
			request.Header.Set("Authorization", "Bearer "+notificationToken)
			request.Header.Set("Content-Type", tc.contentType)
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != tc.want || receiver.calls != 0 {
				t.Fatalf("status=%d want=%d calls=%d body=%s", response.Code, tc.want, receiver.calls, response.Body.String())
			}
		})
	}
}

func TestPublicationCredentialDoesNotAuthorizeDashboardActions(t *testing.T) {
	receiver := &publicationReceiver{}
	updater := &fakeServiceUpdater{}
	server := NewWithActions(fakeSource{}, nil, updater, 1)
	if err := server.ConfigurePublicationNotifications(notificationToken, []string{"ghcr.io/acme/app"}, receiver); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/updates/check", "/api/projects/app/update", "/api/containers/id/update", "/api/containers/id/pull"} {
		request := httptest.NewRequest("POST", "http://localhost"+path, nil)
		request.Header.Set("Authorization", "Bearer "+notificationToken)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != 403 {
			t.Errorf("%s status=%d want403", path, response.Code)
		}
	}
	if len(updater.updates) != 0 || len(updater.pulls) != 0 || receiver.calls != 0 {
		t.Fatal("notification token authorized mutation")
	}
}

func TestPublicationEndpointRejectsBrowserAndUntrustedHost(t *testing.T) {
	for _, tc := range []struct{ host, origin, fetchSite string }{{"localhost", "http://localhost", ""}, {"localhost", "", "same-origin"}, {"attacker.example", "", ""}} {
		receiver := &publicationReceiver{}
		server := New(fakeSource{}, 1)
		if err := server.ConfigurePublicationNotifications(notificationToken, []string{"ghcr.io/acme/app"}, receiver); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", "http://"+tc.host+"/api/images/published", strings.NewReader(publicationBody(t)))
		request.Header.Set("Authorization", "Bearer "+notificationToken)
		request.Header.Set("Content-Type", "application/json")
		if tc.origin != "" {
			request.Header.Set("Origin", tc.origin)
		}
		if tc.fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", tc.fetchSite)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != 403 || receiver.calls != 0 {
			t.Fatalf("request=%+v status=%d calls=%d", tc, response.Code, receiver.calls)
		}
	}
}

func TestPublicationEndpointAcknowledgesRetryAndReportsStorageFailure(t *testing.T) {
	for _, tc := range []struct {
		result string
		err    error
		want   int
	}{{"duplicate", nil, 200}, {"stale", nil, 200}, {"", errors.New("secret filesystem path"), 503}} {
		receiver := &publicationReceiver{result: tc.result, err: tc.err}
		server := New(fakeSource{}, 1)
		if err := server.ConfigurePublicationNotifications(notificationToken, []string{"ghcr.io/acme/app"}, receiver); err != nil {
			t.Fatal(err)
		}
		response := publicationRequest(t, server, publicationBody(t), notificationToken)
		if response.Code != tc.want || strings.Contains(response.Body.String(), "secret filesystem") {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
}

func TestPublicationConfigurationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		token    string
		repos    []string
		receiver PublicationReceiver
	}{
		{"short", []string{"ghcr.io/acme/app"}, &publicationReceiver{}}, {notificationToken, nil, &publicationReceiver{}},
		{notificationToken, []string{"ghcr.io/acme/app:latest"}, &publicationReceiver{}},
		{notificationToken, []string{"ghcr.io/acme/*"}, &publicationReceiver{}}, {notificationToken, []string{"ghcr.io/acme/app"}, nil},
	} {
		server := New(fakeSource{}, 1)
		if err := server.ConfigurePublicationNotifications(tc.token, tc.repos, tc.receiver); err == nil {
			t.Fatal("accepted invalid notification config")
		}
		if response := publicationRequest(t, server, publicationBody(t), notificationToken); response.Code != 503 {
			t.Fatal("invalid config enabled endpoint")
		}
	}
}
