package githubapp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestRemovePublisherSecretFailsClosed(t *testing.T) {
	app, _ := testApp(t)
	for _, tc := range []struct {
		name         string
		deleteStatus int
		readStatus   int
		readName     string
		transport    string
		malformed    bool
	}{
		{name: "delete forbidden", deleteStatus: 403},
		{name: "delete unavailable", deleteStatus: 503},
		{name: "unexpected delete success", deleteStatus: 200},
		{name: "delete transport", transport: "DELETE"},
		{name: "retained after delete", deleteStatus: 204, readStatus: 200, readName: secretName},
		{name: "retained after missing", deleteStatus: 404, readStatus: 200, readName: secretName},
		{name: "read forbidden", deleteStatus: 204, readStatus: 403},
		{name: "read unavailable after missing", deleteStatus: 404, readStatus: 503},
		{name: "read transport", deleteStatus: 204, transport: "GET"},
		{name: "wrong readback name", deleteStatus: 204, readStatus: 200, readName: "OTHER_SECRET"},
		{name: "missing readback name", deleteStatus: 204, readStatus: 200},
		{name: "malformed readback", deleteStatus: 204, readStatus: 200, malformed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var methods []string
			c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/repos/owner/repo/actions/secrets/SERVICES_PUBLISH_TOKEN" {
					t.Fatalf("unrelated secret or variable touched: %s %s", r.Method, r.URL.Path)
				}
				methods = append(methods, r.Method)
				if r.Method == tc.transport {
					return nil, errors.New("sensitive-provider-error")
				}
				if r.Method == "DELETE" {
					return response(tc.deleteStatus, map[string]string{"message": "sensitive-provider-body"}), nil
				}
				if r.Method != "GET" {
					t.Fatalf("unexpected method: %s", r.Method)
				}
				if tc.malformed {
					resp := response(200, nil)
					resp.Body = io.NopCloser(strings.NewReader("not-json-sensitive"))
					return resp, nil
				}
				return response(tc.readStatus, map[string]string{"name": tc.readName, "message": "sensitive-provider-body"}), nil
			}))
			err := c.RemovePublisherSecret(context.Background(), app, repository)
			if err == nil || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("must fail closed with sanitized error: %v", err)
			}
			want := []string{"DELETE"}
			if tc.deleteStatus == 204 || tc.deleteStatus == 404 {
				want = append(want, "GET")
			}
			if !reflect.DeepEqual(methods, want) {
				t.Fatalf("continued after error: got %v want %v", methods, want)
			}
		})
	}
}

func TestRemovePublisherSecretRejectsRepositoryIdentityMismatch(t *testing.T) {
	app, _ := testApp(t)
	for _, field := range []string{"id", "full_name", "default_branch"} {
		t.Run(field, func(t *testing.T) {
			c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/app/installations/7/access_tokens":
					return tokenResponse(), nil
				case "/repos/owner/repo":
					if r.Method != "GET" {
						t.Fatalf("unexpected identity method: %s", r.Method)
					}
					identity := map[string]any{"id": 1, "full_name": "owner/repo", "default_branch": "main"}
					if field == "id" {
						identity[field] = 2
					} else {
						identity[field] = "other"
					}
					return response(200, identity), nil
				default:
					t.Fatalf("secret accessed before identity validation: %s %s", r.Method, r.URL.Path)
					return nil, nil
				}
			})
			if err := c.RemovePublisherSecret(context.Background(), app, repository); err == nil {
				t.Fatal("identity mismatch accepted")
			}
		})
	}
}

func TestRemovePublisherSecretRetriesUncertainDeletion(t *testing.T) {
	app, _ := testApp(t)
	var methods []string
	present := true
	c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/repos/owner/repo/actions/secrets/SERVICES_PUBLISH_TOKEN" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		methods = append(methods, r.Method)
		if r.Method == "DELETE" {
			if present {
				present = false
				return nil, errors.New("sensitive-lost-delete-response")
			}
			return response(404, nil), nil
		}
		if r.Method != "GET" {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		return response(404, nil), nil
	}))
	if err := c.RemovePublisherSecret(context.Background(), app, repository); err == nil || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("uncertain deletion must fail: %v", err)
	}
	if err := c.RemovePublisherSecret(context.Background(), app, repository); err != nil {
		t.Fatalf("retry must verify absence: %v", err)
	}
	if want := []string{"DELETE", "DELETE", "GET"}; !reflect.DeepEqual(methods, want) {
		t.Fatalf("requests: got %v want %v", methods, want)
	}
}
