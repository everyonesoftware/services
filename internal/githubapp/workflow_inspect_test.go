package githubapp

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestInspectRecognizesGeneratedWorkflowAtItsOriginalPath(t *testing.T) {
	app, _ := testApp(t)
	sha := baseSHA
	files := map[string]string{".github/workflows/publish.yml": conventionalWorkflow}
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || !plan.Supported {
		t.Fatal(plan, err)
	}
	for _, f := range plan.Files {
		files[f.Path] = f.After
	}
	fallback := planTransport(t, files, &sha, nil)
	c = fakeClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/contents/") && r.URL.Query().Get("ref") == "main" {
			path := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/contents/")
			s, ok := files[path]
			if !ok {
				return response(404, nil), nil
			}
			return response(200, map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(s))}), nil
		}
		return fallback(r)
	})
	inspection, err := c.Inspect(context.Background(), app, repository)
	if err != nil || !inspection.Supported || len(inspection.Problems) != 0 {
		t.Fatalf("%+v %v", inspection, err)
	}
}
