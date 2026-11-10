package githubapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func fixtureFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, p := range []string{".github/workflows/verify-and-publish.yml", "scripts/publish-notification.mjs", ".github/workflows/services-connection-test.yml", "scripts/publisher-connection-test.mjs"} {
		b, e := os.ReadFile("../../" + p)
		if e != nil {
			t.Fatal(e)
		}
		files[p] = string(b)
	}
	return files
}
func TestInspectRecognizesExistingHooksAtDefaultBranch(t *testing.T) {
	app, _ := testApp(t)
	files := fixtureFiles(t)
	reads := 0
	c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		reads++
		if r.URL.Query().Get("ref") != "main" {
			t.Fatal("not default branch")
		}
		p := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/contents/")
		s, ok := files[p]
		if !ok {
			t.Fatalf("unexpected file %s", p)
		}
		return response(200, map[string]any{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(s))}), nil
	}))
	got, e := c.Inspect(context.Background(), app, repository)
	if e != nil || !got.Supported || len(got.Problems) != 0 || reads != 4 {
		t.Fatalf("%+v %v reads=%d", got, e, reads)
	}
}
func TestInspectReportsMissingAndUnsupportedHooks(t *testing.T) {
	app, _ := testApp(t)
	cases := []struct{ path, old, new string }{
		{".github/workflows/verify-and-publish.yml", "node scripts/publish-notification.mjs capture", "echo skipped"},
		{"scripts/publish-notification.mjs", "['capture', 'send'].includes(args[0])", "['capture'].includes(args[0])"},
		{".github/workflows/services-connection-test.yml", "      challenge:", "      wrong_input:"},
		{".github/workflows/services-connection-test.yml", "secrets.SERVICES_PUBLISH_TOKEN", "secrets.OTHER"},
		{"scripts/publisher-connection-test.mjs", "/api/publishers/callback", "/wrong"},
		{"scripts/publisher-connection-test.mjs", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.old, func(t *testing.T) {
			files := fixtureFiles(t)
			if tc.old != "" {
				files[tc.path] = strings.ReplaceAll(files[tc.path], tc.old, tc.new)
			} else {
				delete(files, tc.path)
			}
			c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
				s, ok := files[strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/contents/")]
				if !ok {
					return response(404, nil), nil
				}
				return response(200, map[string]any{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(s))}), nil
			}))
			got, e := c.Inspect(context.Background(), app, repository)
			if e != nil || got.Supported || len(got.Problems) == 0 {
				t.Fatalf("%+v %v", got, e)
			}
		})
	}
}
func TestDispatchTestPrecisePayload(t *testing.T) {
	app, _ := testApp(t)
	called := false
	c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		called = true
		if r.Method != "POST" || r.URL.Path != "/repos/owner/repo/actions/workflows/services-connection-test.yml/dispatches" {
			t.Fatalf("%s %s", r.Method, r.URL.Path)
		}
		var v struct {
			Ref    string            `json:"ref"`
			Inputs map[string]string `json:"inputs"`
		}
		if e := json.NewDecoder(r.Body).Decode(&v); e != nil {
			t.Fatal(e)
		}
		if v.Ref != "main" || len(v.Inputs) != 2 || v.Inputs["connection_id"] != "connection_1" || v.Inputs["challenge"] != strings.Repeat("c", 32) {
			t.Fatalf("%+v", v)
		}
		return response(204, nil), nil
	}))
	if e := c.DispatchTest(context.Background(), app, repository, "connection_1", strings.Repeat("c", 32)); e != nil || !called {
		t.Fatalf("%v called=%v", e, called)
	}
}
func TestRepositoryIdentityMismatchBlocksMutations(t *testing.T) {
	app, _ := testApp(t)
	bad := repository
	bad.DefaultBranch = "stale"
	writes := 0
	c := fakeClient(t, repositoryTransport(t, func(*http.Request) (*http.Response, error) { writes++; return response(204, nil), nil }))
	if e := c.DispatchTest(context.Background(), app, bad, "connection", strings.Repeat("x", 32)); e == nil || writes != 0 {
		t.Fatalf("%v writes=%d", e, writes)
	}
}
