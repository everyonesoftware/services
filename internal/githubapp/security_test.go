package githubapp

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPKCS8AndMalformedAppCredentials(t *testing.T) {
	app, key := testApp(t)
	der, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	app.PEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	c := fakeClient(t, func(*http.Request) (*http.Response, error) { return response(200, []any{}), nil })
	if _, e := c.Repositories(context.Background(), app); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*App){func(a *App) { a.PEM += "trailing-sensitive" }, func(a *App) { a.Slug = "../bad" }, func(a *App) { a.ID = -1 }, func(a *App) { a.PEM = strings.Repeat("s", 33<<10) }} {
		bad := app
		mutate(&bad)
		c := fakeClient(t, func(*http.Request) (*http.Response, error) { t.Fatal("bad credentials sent"); return nil, nil })
		if _, e := c.Repositories(context.Background(), bad); e == nil || strings.Contains(e.Error(), "sensitive") {
			t.Fatalf("%v", e)
		}
	}
}
func TestRequestBoundsAndMalformedResponses(t *testing.T) {
	for _, body := range []string{strings.Repeat("sensitive", (2<<20)/9+2), `{"sensitive":"`, `null`} {
		c := fakeClient(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 201, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if _, e := c.ExchangeManifest(context.Background(), "abc"); e == nil || strings.Contains(e.Error(), "sensitive") {
			t.Fatalf("%v", e)
		}
	}
}
func TestClientClampsTimeoutWithoutMutatingCaller(t *testing.T) {
	input := &http.Client{Timeout: time.Hour}
	c := NewClient(input)
	if input.Timeout != time.Hour || c.httpClient.Timeout != 15*time.Second || c.httpClient.CheckRedirect == nil {
		t.Fatal("timeout/redirect configuration")
	}
	short := NewClient(&http.Client{Timeout: time.Second})
	if short.httpClient.Timeout != time.Second {
		t.Fatal("shorter timeout was enlarged")
	}
}
func TestRepositoriesGlobalRequestBudget(t *testing.T) {
	app, _ := testApp(t)
	calls := 0
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		switch {
		case r.URL.Path == "/app/installations":
			batch := []any{}
			if r.URL.Query().Get("page") == "1" {
				for i := 1; i <= 100; i++ {
					batch = append(batch, map[string]any{"id": i})
				}
			}
			return response(200, batch), nil
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			return tokenResponse(), nil
		default:
			return response(200, map[string]any{"repositories": []any{}}), nil
		}
	})
	repos, e := c.Repositories(context.Background(), app)
	if e == nil || repos != nil || calls > 200 {
		t.Fatalf("%v calls=%d", e, calls)
	}
}
func TestInspectEmptyFilesAreUnsupported(t *testing.T) {
	app, _ := testApp(t)
	c := fakeClient(t, repositoryTransport(t, func(*http.Request) (*http.Response, error) {
		return response(200, map[string]any{"type": "file", "encoding": "base64", "content": ""}), nil
	}))
	got, e := c.Inspect(context.Background(), app, repository)
	if e != nil || got.Supported || len(got.Problems) == 0 {
		t.Fatalf("%+v %v", got, e)
	}
}
func TestInspectUsesEncodedDefaultBranchAndNeverDownloadURL(t *testing.T) {
	app, _ := testApp(t)
	repo := repository
	repo.DefaultBranch = "release/one"
	reads := 0
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/app/installations/7/access_tokens":
			return tokenResponse(), nil
		case "/repos/owner/repo":
			return response(200, map[string]any{"id": 1, "full_name": "owner/repo", "default_branch": repo.DefaultBranch}), nil
		default:
			reads++
			if r.URL.Query().Get("ref") != repo.DefaultBranch {
				t.Fatalf("bad ref %s", r.URL.RawQuery)
			}
			return response(200, map[string]any{"type": "symlink", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("x")), "download_url": "https://evil.example/"}), nil
		}
	})
	got, e := c.Inspect(context.Background(), app, repo)
	if e != nil || got.Supported || reads != 4 {
		t.Fatalf("%+v %v", got, e)
	}
}
func TestDispatchInvalidInputsNeverRequest(t *testing.T) {
	app, _ := testApp(t)
	c := fakeClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid input sent")
		return nil, fmt.Errorf("unexpected")
	})
	for _, v := range [][2]string{{"../x", strings.Repeat("a", 32)}, {"ok", "short"}, {"ok", strings.Repeat("a", 513)}} {
		if e := c.DispatchTest(context.Background(), app, repository, v[0], v[1]); e == nil {
			t.Fatal("invalid test accepted")
		}
	}
}
