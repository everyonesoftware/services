package githubapp

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestOperationTokensHaveExplicitLeastPrivilege(t *testing.T) {
	app, _ := testApp(t)
	for _, tc := range []struct {
		name        string
		permissions map[string]string
		run         func(*Client)
	}{
		{"discovery", map[string]string{"metadata": "read"}, func(c *Client) { _, _ = c.Repositories(context.Background(), app) }},
		{"plan", map[string]string{"metadata": "read", "contents": "read"}, func(c *Client) { _, _ = c.PlanWorkflow(context.Background(), app, repository) }},
		{"inspect", map[string]string{"metadata": "read", "contents": "read"}, func(c *Client) { _, _ = c.Inspect(context.Background(), app, repository) }},
		{"install", map[string]string{"metadata": "read", "contents": "write", "workflows": "write", "pull_requests": "write"}, func(c *Client) {
			_, _ = c.InstallWorkflow(context.Background(), app, repository, strings.Repeat("a", 64))
		}},
		{"configure", map[string]string{"metadata": "read", "secrets": "write", "variables": "write"}, func(c *Client) {
			_ = c.Configure(context.Background(), app, repository, strings.Repeat("s", 32), map[string]string{enabledName: "false"})
		}},
		{"recover", map[string]string{"metadata": "read", "secrets": "write", "variables": "write"}, func(c *Client) {
			_ = c.RecoverConfiguration(context.Background(), app, repository, strings.Repeat("s", 32), map[string]string{enabledName: "false"})
		}},
		{"rotate", map[string]string{"metadata": "read", "secrets": "write"}, func(c *Client) { _ = c.ReplaceSecret(context.Background(), app, repository, strings.Repeat("s", 32)) }},
		{"enable", map[string]string{"metadata": "read", "variables": "write"}, func(c *Client) { _ = c.SetEnabled(context.Background(), app, repository, true) }},
		{"dispatch", map[string]string{"metadata": "read", "actions": "write"}, func(c *Client) {
			_ = c.DispatchTest(context.Background(), app, repository, "connection", strings.Repeat("a", 16))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := false
			c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/app/installations" {
					return response(200, []map[string]int64{{"id": repository.InstallationID}}), nil
				}
				if strings.HasSuffix(r.URL.Path, "/access_tokens") {
					captured = true
					var input struct {
						Permissions   map[string]string `json:"permissions"`
						RepositoryIDs []int64           `json:"repository_ids"`
					}
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Fatalf("missing explicit permissions: %v", err)
					}
					if !reflect.DeepEqual(input.Permissions, tc.permissions) {
						t.Errorf("permissions=%v want=%v", input.Permissions, tc.permissions)
					}
					if tc.name != "discovery" && !reflect.DeepEqual(input.RepositoryIDs, []int64{repository.ID}) {
						t.Errorf("repository scope=%v", input.RepositoryIDs)
					}
				}
				return response(403, nil), nil
			})
			tc.run(c)
			if !captured {
				t.Fatal("token request not exercised")
			}
		})
	}
}
