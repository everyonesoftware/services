package githubapp

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestRepositoriesMalformedPagesFailClosed(t *testing.T) {
	app, _ := testApp(t)
	for _, stage := range []string{"installations", "repositories", "later-page"} {
		t.Run(stage, func(t *testing.T) {
			c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
				switch {
				case r.URL.Path == "/app/installations":
					if stage == "installations" {
						return response(200, nil), nil
					}
					return response(200, []any{map[string]any{"id": 7}}), nil
				case strings.HasSuffix(r.URL.Path, "/access_tokens"):
					return tokenResponse(), nil
				default:
					if stage == "repositories" {
						return response(200, map[string]any{"other": "data"}), nil
					}
					if r.URL.Query().Get("page") == "1" {
						v := response(200, map[string]any{"repositories": []any{map[string]any{"id": 1, "full_name": "owner/repo", "default_branch": "main"}}})
						v.Header.Set("Link", `<https://api.github.com/next>; rel="next"`)
						return v, nil
					}
					return response(500, map[string]any{"message": "sensitive-body"}), nil
				}
			})
			repos, e := c.Repositories(context.Background(), app)
			if e == nil || repos != nil || strings.Contains(e.Error(), "sensitive") {
				t.Fatalf("%v %v", repos, e)
			}
		})
	}
}
