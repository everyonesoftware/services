package githubapp

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

const baseSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const treeSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func planTransport(t *testing.T, files map[string]string, sha *string, extra transportFunc) transportFunc {
	t.Helper()
	return repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		p := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo")
		if r.Method == "GET" && p == "/git/ref/heads/main" {
			return response(200, map[string]any{"ref": "refs/heads/main", "object": map[string]string{"type": "commit", "sha": *sha}}), nil
		}
		if r.Method == "GET" && p == "/git/commits/"+*sha {
			return response(200, map[string]any{"sha": *sha, "tree": map[string]string{"sha": treeSHA}}), nil
		}
		if r.Method == "GET" && strings.HasPrefix(p, "/contents/") {
			if r.URL.Query().Get("ref") != *sha {
				if extra != nil {
					return extra(r)
				}
				t.Fatal("content not pinned to actual base SHA")
			}
			path := strings.TrimPrefix(p, "/contents/")
			if path == ".github/workflows" {
				entries := []map[string]string{}
				for f := range files {
					if strings.HasPrefix(f, ".github/workflows/") {
						entries = append(entries, map[string]string{"type": "file", "path": f, "name": strings.TrimPrefix(f, ".github/workflows/")})
					}
				}
				return response(200, entries), nil
			}
			content, ok := files[path]
			if !ok {
				return response(404, nil), nil
			}
			return response(200, map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))}), nil
		}
		if extra != nil {
			return extra(r)
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		return nil, nil
	})
}

func TestPlanPreservesExistingHookAndAddsOnlyMissingConnectionFiles(t *testing.T) {
	app, _ := testApp(t)
	files := fixtureFiles(t)
	delete(files, ".github/workflows/services-connection-test.yml")
	delete(files, "scripts/publisher-connection-test.mjs")
	sha := baseSHA
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || !plan.Supported || len(plan.Files) != 2 || len(plan.Fingerprint) != 64 {
		t.Fatalf("%+v %v", plan, err)
	}
	for _, f := range plan.Files {
		if f.Before != "" || f.After == "" || strings.Contains(f.Path, "publish-notification") || strings.Contains(f.Path, "verify-and-publish") {
			t.Fatalf("duplicated or replaced hook: %+v", f)
		}
	}
	again, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || again.Fingerprint != plan.Fingerprint {
		t.Fatal("unstable fingerprint", err)
	}
	sha = strings.Repeat("c", 40)
	changed, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || changed.Fingerprint == plan.Fingerprint {
		t.Fatal("base SHA not bound", err)
	}
}
