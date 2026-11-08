package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHelperUsesExactImageMountsAndNoInheritedSecrets(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/containers/create") {
			json.NewDecoder(r.Body).Decode(&payload)
			w.WriteHeader(201)
			w.Write([]byte(`{"Id":"helper"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/start") {
			w.WriteHeader(204)
			return
		}
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	}))
	defer server.Close()
	e := NewEngine(server.URL, server.Client())
	c := WorkerConfig{Image: "sha256:exact", User: "10001:10001", DataDir: "/data", Volume: "services-data", HostRoot: "C:/Code", MountRoot: "/projects", Socket: "/var/run/docker.sock", SocketSource: "/var/run/docker.sock", ProjectSource: "C:/Code", Project: "services", Service: "services"}
	if err := e.Launch(context.Background(), c, Operation{ID: "compose-123", Worker: "services-worker-123"}); err != nil {
		t.Fatal(err)
	}
	if payload["Image"] != "sha256:exact" {
		t.Fatalf("mutable image: %+v", payload)
	}
	raw, _ := json.Marshal(payload)
	if strings.Contains(string(raw), "AutoRemove\":true") || strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), "PortBindings") {
		t.Fatalf("unsafe helper: %s", raw)
	}
	if payload["User"] != "10001:10001" {
		t.Fatal("worker must retain app UID")
	}
	host := payload["HostConfig"].(map[string]any)
	mounts := host["Mounts"].([]any)
	if len(mounts) != 3 {
		t.Fatalf("mounts: %v", mounts)
	}
	if mounts[0].(map[string]any)["Source"] != "services-data" {
		t.Fatal("wrong durable volume")
	}
}
