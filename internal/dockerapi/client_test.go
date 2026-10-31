package dockerapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestContainersReadsComposeLabelsAndHealthFromEngine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"ApiVersion":"1.47"}`))
		case "/v1.47/containers/json":
			_, _ = w.Write([]byte(`[{"Id":"abc","Names":["/web-1"],"State":"running","Image":"jellyfin/jellyfin:latest","Labels":{"com.docker.compose.project":"shop","com.docker.compose.service":"web"}}]`))
		case "/v1.47/containers/abc/json":
			_, _ = w.Write([]byte(`{"Config":{"Image":"nginx:latest"},"State":{"Status":"running","Health":{"Status":"healthy"}}}`))
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := New(server.URL, server.Client())
	containers, err := client.Containers()
	if err != nil {
		t.Fatalf("Containers() error = %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("got %d containers, want 1", len(containers))
	}
	got := containers[0]
	if got.Project != "shop" || got.Service != "web" || got.State != "running" || got.Health != "healthy" {
		t.Fatalf("container = %+v, want Compose metadata and healthy state", got)
	}
	if got.Image != "jellyfin/jellyfin:latest" {
		t.Fatalf("container image = %q, want jellyfin/jellyfin:latest", got.Image)
	}
}
