package operations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLaunchRejectsInvalidStorageBeforeCallingDocker(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write([]byte(`{"Id":"helper"}`)) }))
	defer server.Close()
	engine := NewEngine(server.URL, server.Client())
	for _, storage := range []StorageMount{{}, {Type: "bind", Source: "./data"}, {Type: "bind", Source: "/"}, {Type: "tmpfs", Source: "tmpfs"}} {
		if err := engine.Launch(context.Background(), WorkerConfig{DataDir: "/data", Storage: storage}, Operation{Worker: "worker"}); err == nil {
			t.Errorf("invalid mount accepted: %+v", storage)
		}
	}
	if requests != 0 {
		t.Fatalf("unsafe helper payload reached Docker: %d requests", requests)
	}
}
