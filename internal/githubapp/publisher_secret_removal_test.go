package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

func TestRemovePublisherSecretAlreadyMissingAndRetry(t *testing.T) {
	app, _ := testApp(t)
	for _, initiallyPresent := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "retry"}[initiallyPresent], func(t *testing.T) {
			present := initiallyPresent
			var methods []string
			c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/repos/owner/repo/actions/secrets/SERVICES_PUBLISH_TOKEN" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				methods = append(methods, r.Method)
				if r.Method == "DELETE" {
					if present {
						present = false
						return response(204, nil), nil
					}
					return response(404, nil), nil
				}
				if r.Method != "GET" {
					t.Fatalf("unexpected method: %s", r.Method)
				}
				return response(404, nil), nil
			}))
			for attempt := 0; attempt < 2; attempt++ {
				if err := c.RemovePublisherSecret(context.Background(), app, repository); err != nil {
					t.Fatalf("attempt %d: %v", attempt, err)
				}
			}
			if want := []string{"DELETE", "GET", "DELETE", "GET"}; !reflect.DeepEqual(methods, want) {
				t.Fatalf("methods: got %v want %v", methods, want)
			}
		})
	}
}

func TestRemovePublisherSecretScopesTokenAndVerifiesAbsence(t *testing.T) {
	app, _ := testApp(t)
	var calls []string
	remote := repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/repos/owner/repo/actions/secrets/SERVICES_PUBLISH_TOKEN" || r.URL.RawQuery != "" {
			t.Fatalf("unexpected secret or variable request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer installation-token" {
			t.Fatal("missing scoped installation token")
		}
		switch r.Method {
		case "DELETE":
			return response(204, nil), nil
		case "GET":
			return response(404, nil), nil
		default:
			t.Fatalf("unexpected method: %s", r.Method)
			return nil, nil
		}
	})
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/app/installations/7/access_tokens" {
			if r.Method != "POST" {
				t.Fatalf("unexpected token method: %s", r.Method)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"repository_ids": []any{float64(1)}, "permissions": map[string]any{"metadata": "read", "secrets": "write"}}
			if !reflect.DeepEqual(payload, want) {
				t.Fatalf("token scope: got %v want %v", payload, want)
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		return remote(r)
	})
	if err := c.RemovePublisherSecret(context.Background(), app, repository); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /app/installations/7/access_tokens",
		"GET /repos/owner/repo",
		"DELETE /repos/owner/repo/actions/secrets/SERVICES_PUBLISH_TOKEN",
		"GET /repos/owner/repo/actions/secrets/SERVICES_PUBLISH_TOKEN",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("requests: got %v want %v", calls, want)
	}
}
