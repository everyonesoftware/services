package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverAndLaunchPreserveReadOnlySocketAndSupplementaryGroups(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/containers/self/json":
			w.Write([]byte(`{"Id":"self","Image":"sha256:exact","Config":{"User":"10001:10001","Labels":{"com.docker.compose.project":"services","com.docker.compose.service":"services"}},"HostConfig":{"GroupAdd":["0","docker"]},"State":{"Running":true},"Mounts":[{"Type":"volume","Name":"services-data","Destination":"/data","RW":true},{"Type":"bind","Source":"C:/Code","Destination":"/projects","RW":false},{"Type":"bind","Source":"/var/run/docker.sock","Destination":"/var/run/docker.sock","RW":false}]}`))
		case r.Method == "POST" && r.URL.Path == "/containers/create":
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"Id":"helper"}`))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/start"):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	engine := NewEngine(server.URL, server.Client())
	config, err := engine.Discover(context.Background(), "self", "/data", "C:/Code", "/projects", "/var/run/docker.sock", "services", "services")
	if err != nil {
		t.Fatalf("realistic read-only socket deployment rejected: %v", err)
	}
	if err := engine.Launch(context.Background(), config, Operation{ID: "compose-123", Worker: "services-worker-123"}); err != nil {
		t.Fatal(err)
	}
	if payload["User"] != "10001:10001" {
		t.Fatalf("worker user: %v", payload["User"])
	}
	host := payload["HostConfig"].(map[string]any)
	if !reflect.DeepEqual(host["GroupAdd"], []any{"0", "docker"}) {
		t.Fatalf("supplementary groups lost: %v", host["GroupAdd"])
	}
	mounts := host["Mounts"].([]any)
	socket := mounts[2].(map[string]any)
	if socket["Source"] != "/var/run/docker.sock" || socket["ReadOnly"] != true {
		t.Fatalf("socket mount changed: %v", socket)
	}
}

func TestHelperDisablesInheritedHTTPHealthcheck(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/create" {
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			w.Write([]byte(`{"Id":"helper"}`))
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	if err := NewEngine(server.URL, server.Client()).Launch(context.Background(), WorkerConfig{Image: "sha256:exact"}, Operation{Worker: "helper"}); err != nil {
		t.Fatal(err)
	}
	health, ok := payload["Healthcheck"].(map[string]any)
	if !ok || !reflect.DeepEqual(health["Test"], []any{"NONE"}) {
		t.Fatalf("worker inherited app HTTP healthcheck: %v", payload["Healthcheck"])
	}
}
