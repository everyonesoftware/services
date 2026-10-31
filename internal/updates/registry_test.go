package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestManifestDigestFollowsReadOnlyBearerChallenge(t *testing.T) {
	const digest = "sha256:0123456789abcdef"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			if r.Method != http.MethodGet || r.URL.Query().Get("scope") != "repository:library/nginx:pull" {
				t.Errorf("token request = %s %s, want GET with repository pull scope", r.Method, r.URL.String())
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "read-only-token"})
		case r.URL.Path == "/v2/library/nginx/manifests/latest":
			if r.Method != http.MethodHead {
				t.Errorf("manifest method = %s, want HEAD", r.Method)
			}
			if !strings.Contains(r.Header.Get("Accept"), "application/vnd.oci.image.index.v1+json") {
				t.Errorf("manifest Accept = %q, want OCI manifest types", r.Header.Get("Accept"))
			}
			if r.Header.Get("Authorization") != "Bearer read-only-token" {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm=%q,service=%q,scope=%q`, server.URL+"/token", "registry.docker.io", "repository:library/nginx:pull"))
				http.Error(w, "authentication required", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Docker-Content-Digest", digest)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := newRegistryClient(server.Client(), map[string]string{"registry-1.docker.io": parsed.String()})
	got, err := client.ManifestDigest(context.Background(), "nginx:latest")
	if err != nil {
		t.Fatalf("ManifestDigest() error = %v", err)
	}
	if got != digest {
		t.Fatalf("ManifestDigest() = %q, want %q", got, digest)
	}
}
