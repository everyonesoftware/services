package githubapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func tokenResponse() *http.Response {
	return response(201, map[string]any{"token": "installation-token", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
}
func TestRepositoriesPaginatesInstallationsAndRepositories(t *testing.T) {
	app, _ := testApp(t)
	calls := 0
	installation := 7
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		switch r.URL.Path {
		case "/app/installations":
			if r.URL.Query().Get("page") == "1" {
				v := response(200, []any{map[string]any{"id": 7}})
				v.Header.Set("Link", `<https://evil.example/next>; rel="next"`)
				return v, nil
			}
			return response(200, []any{map[string]any{"id": 8}}), nil
		case "/app/installations/7/access_tokens":
			return tokenResponse(), nil
		case "/app/installations/8/access_tokens":
			installation = 8
			return tokenResponse(), nil
		case "/installation/repositories":
			if r.Header.Get("Authorization") != "Bearer installation-token" {
				t.Fatal("installation auth missing")
			}
			if installation == 7 && r.URL.Query().Get("page") == "1" {
				v := response(200, map[string]any{"repositories": []any{map[string]any{"id": 1, "full_name": "owner/one", "default_branch": "main"}}})
				v.Header.Set("Link", `<https://api.github.com/installation/repositories?page=2>; rel="next"`)
				return v, nil
			}
			if installation == 7 {
				return response(200, map[string]any{"repositories": []any{map[string]any{"id": 2, "full_name": "owner/two", "default_branch": "release/one"}}}), nil
			}
			return response(200, map[string]any{"repositories": []any{map[string]any{"id": 3, "full_name": "other/three", "default_branch": "main"}}}), nil
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
			return nil, nil
		}
	})
	got, e := c.Repositories(context.Background(), app)
	if e != nil || len(got) != 3 || got[0].InstallationID != 7 || got[2].InstallationID != 8 || calls != 7 {
		t.Fatalf("%+v %v calls=%d", got, e, calls)
	}
}
func TestRepositoriesPageLimitFailsWithoutPartialResults(t *testing.T) {
	app, _ := testApp(t)
	calls := 0
	c := fakeClient(t, func(*http.Request) (*http.Response, error) {
		calls++
		v := response(200, []any{})
		v.Header.Set("Link", `<https://api.github.com/next>; rel="next"`)
		return v, nil
	})
	got, e := c.Repositories(context.Background(), app)
	if e == nil || got != nil || calls > 20 {
		t.Fatalf("unbounded/partial result: %v %v %d", got, e, calls)
	}
}
func TestRepositoriesRejectsInvalidTokenAndPartialFailure(t *testing.T) {
	app, _ := testApp(t)
	for _, bad := range []any{map[string]any{"token": "", "expires_at": time.Now().Add(time.Hour)}, map[string]any{"token": "sensitive", "expires_at": "2000-01-01T00:00:00Z"}} {
		c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/app/installations" {
				return response(200, []any{map[string]any{"id": 7}}), nil
			}
			return response(201, bad), nil
		})
		got, e := c.Repositories(context.Background(), app)
		if e == nil || got != nil || strings.Contains(e.Error(), "sensitive") {
			t.Fatalf("%v %v", got, e)
		}
	}
}

var repository = Repository{ID: 1, FullName: "owner/repo", DefaultBranch: "main", InstallationID: 7}

// repositoryTransport models the provider's authoritative repository identity.
func repositoryTransport(t *testing.T, f transportFunc) transportFunc {
	t.Helper()
	return func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/app/installations/7/access_tokens":
			b, _ := io.ReadAll(r.Body)
			var v struct {
				RepositoryIDs []int64 `json:"repository_ids"`
			}
			if e := json.Unmarshal(b, &v); e != nil || len(v.RepositoryIDs) != 1 || v.RepositoryIDs[0] != 1 {
				t.Fatalf("unscoped token: %s", b)
			}
			return tokenResponse(), nil
		case "/repos/owner/repo":
			return response(200, map[string]any{"id": 1, "full_name": "owner/repo", "default_branch": "main"}), nil
		default:
			return f(r)
		}
	}
}
