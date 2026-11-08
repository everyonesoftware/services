package dockerapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestContainersRetainConfiguredTagAfterLocalTagMoves(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			w.Write([]byte(`{"ApiVersion":"1.47"}`))
		case "/v1.47/containers/json":
			w.Write([]byte(`[{"Id":"old","Names":["/services"],"State":"running","Image":"sha256:oldmanifest","ImageID":"sha256:oldconfig","Labels":{"com.docker.compose.project":"services","com.docker.compose.service":"services"}}]`))
		case "/v1.47/images/json":
			w.Write([]byte(`[]`))
		case "/v1.47/containers/old/json":
			w.Write([]byte(`{"Config":{"Image":"ghcr.io/everyonesoftware/services:latest"},"State":{"Status":"running"}}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	containers, err := New(server.URL, server.Client()).Containers()
	if err != nil || len(containers) != 1 || containers[0].Image != "ghcr.io/everyonesoftware/services:latest" {
		t.Fatalf("lost configured tag when local tag changed: %+v %v", containers, err)
	}
}
