package dockerapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestContainersReadsComposeLabelsAndHealthFromEngine(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			_, _ = w.Write([]byte(`{"ApiVersion":"1.47"}`))
		case "/v1.47/containers/json":
			_, _ = w.Write([]byte(`[{"Id":"abc","Names":["/web-1"],"State":"running","Image":"jellyfin/jellyfin:latest","ImageID":"sha256:image","Labels":{"com.docker.compose.project":"shop","com.docker.compose.service":"web","com.docker.compose.project.working_dir":"E:/Containers/shop","com.docker.compose.project.config_files":"E:/Containers/shop/compose.yaml,E:/Containers/shop/override.yaml"}}]`))
		case "/v1.47/images/json":
			if r.URL.Query().Get("digests") != "1" {
				t.Errorf("images query = %q, want digests=1", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`[{"Id":"sha256:image","Created":1700000000,"RepoDigests":["jellyfin/jellyfin@sha256:manifest"]},{"Id":"sha256:other"}]`))
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
	if len(got.RepoDigests) != 1 || got.RepoDigests[0] != "jellyfin/jellyfin@sha256:manifest" {
		t.Fatalf("container repo digests = %v, want the running image's repository digest", got.RepoDigests)
	}
	if got.ImageCreatedAt != 1700000000 {
		t.Fatalf("container image created timestamp = %d, want 1700000000", got.ImageCreatedAt)
	}
	if got.ComposeWorkingDir != "E:/Containers/shop" || !reflect.DeepEqual(got.ComposeConfigFiles, []string{"E:/Containers/shop/compose.yaml", "E:/Containers/shop/override.yaml"}) || got.ComposeConfigFilesLabel != "E:/Containers/shop/compose.yaml,E:/Containers/shop/override.yaml" {
		t.Fatalf("Compose paths = (%q, %v), want the container's project metadata", got.ComposeWorkingDir, got.ComposeConfigFiles)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal container: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode container JSON: %v", err)
	}
	if _, exists := payload["imageTags"]; exists {
		t.Fatal("container API must not expose the removed local tag list")
	}
}
