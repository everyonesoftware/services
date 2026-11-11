package githubapp

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestInspectDistinguishesUnsupportedWorkflowFromPendingSetup(t *testing.T) {
	app, _ := testApp(t)
	for _, tc := range []struct {
		name, workflow string
		pending        bool
	}{
		{"unsupported", strings.ReplaceAll(conventionalWorkflow, "APP_VERSION=${{ github.run_number }}", "OTHER=unknown"), false},
		{"ready for setup", conventionalWorkflow, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sha := baseSHA
			fallback := planTransport(t, map[string]string{".github/workflows/publish.yml": tc.workflow}, &sha, nil)
			c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
				if r.Method == "GET" && strings.Contains(r.URL.Path, "/contents/") && r.URL.Query().Get("ref") == "main" {
					return response(404, nil), nil
				}
				return fallback(r)
			})
			inspection, err := c.Inspect(context.Background(), app, repository)
			if err != nil || inspection.Supported || len(inspection.Problems) == 0 {
				t.Fatalf("%+v %v", inspection, err)
			}
			message := strings.Join(inspection.Problems, " ")
			if strings.Contains(message, "Merge") != tc.pending {
				t.Fatalf("merge guidance must only accompany a supported setup plan: %s", message)
			}
			if tc.pending && !strings.Contains(message, "Open and review") {
				t.Fatal("guidance assumes a setup PR already exists:", message)
			}
		})
	}
}
