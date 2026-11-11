package githubapp

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestFederationSubjectUsesCurrentRepositoryCustomization(t *testing.T) {
	app, _ := testApp(t)
	repo := Repository{ID: 12, InstallationID: 7, FullName: "acme/app", DefaultBranch: "release:v1"}
	for _, tt := range []struct {
		name    string
		payload map[string]any
		want    string
		fail    bool
	}{
		{"immutable", map[string]any{"use_default": true, "use_immutable_subject": true, "sub_claim_prefix": "repo:acme@123/app@456"}, "repo:acme@123/app@456:ref:refs/heads/release%3Av1", false},
		{"legacy explicit", map[string]any{"use_default": true, "sub_claim_prefix": "repo:acme/app"}, "repo:acme/app:ref:refs/heads/release%3Av1", false},
		{"legacy old API", map[string]any{"use_default": true}, "repo:acme/app:ref:refs/heads/release%3Av1", false},
		{"missing immutable prefix", map[string]any{"use_default": true, "use_immutable_subject": true}, "", true},
		{"custom", map[string]any{"use_default": false, "include_claim_keys": []string{"repo", "context", "job_workflow_ref"}}, "", true},
		{"missing config", map[string]any{}, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					if r.Method != "POST" || r.URL.Path != "/app/installations/7/access_tokens" {
						t.Fatalf("token request: %s %s", r.Method, r.URL.Path)
					}
					var body struct {
						RepositoryIDs []int64           `json:"repository_ids"`
						Permissions   map[string]string `json:"permissions"`
					}
					_ = json.NewDecoder(r.Body).Decode(&body)
					if !reflect.DeepEqual(body.RepositoryIDs, []int64{12}) || !reflect.DeepEqual(body.Permissions, map[string]string{"actions": "read"}) {
						t.Fatalf("token scope: %+v", body)
					}
					return response(201, map[string]any{"token": "installation-token", "expires_at": time.Now().Add(time.Hour)}), nil
				}
				if r.Method != "GET" || r.URL.Path != "/repos/acme/app/actions/oidc/customization/sub" || r.Header.Get("Authorization") != "Bearer installation-token" {
					t.Fatalf("OIDC request: %s %s", r.Method, r.URL.Path)
				}
				return response(200, tt.payload), nil
			})
			// Optional interface assertion keeps the RED test compilable before implementation.
			provider, ok := any(c).(interface {
				FederationSubject(context.Context, App, Repository) (string, error)
			})
			if !ok {
				t.Fatal("missing FederationSubject provider")
			}
			got, err := provider.FederationSubject(context.Background(), app, repo)
			if got != tt.want || (err != nil) != tt.fail || calls != 2 {
				t.Fatalf("subject=%q error=%v calls=%d", got, err, calls)
			}
		})
	}
}
