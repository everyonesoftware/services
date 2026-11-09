package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoverAndLaunchReuseHostDataDirectory(t *testing.T) {
	for _, source := range []string{"/run/desktop/mnt/host/e/Containers/Services/data folder", `E:\Containers\Services\data folder`} {
		t.Run(source, func(t *testing.T) {
			var payload map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/containers/self/json":
					json.NewEncoder(w).Encode(map[string]any{"Id": "self", "Image": "sha256:exact", "Config": map[string]any{"User": "services", "Labels": map[string]string{"com.docker.compose.project": "services", "com.docker.compose.service": "services"}}, "State": map[string]any{"Running": true}, "Mounts": []map[string]any{{"Type": "bind", "Source": source, "Destination": "/data", "RW": true}, {"Type": "bind", "Source": "/projects", "Destination": "/projects", "RW": false}, {"Type": "bind", "Source": "/var/run/docker.sock", "Destination": "/var/run/docker.sock", "RW": false}}})
				case r.Method == "POST" && r.URL.Path == "/containers/create":
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					w.WriteHeader(http.StatusCreated)
					w.Write([]byte(`{"Id":"helper"}`))
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/start"):
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			engine := NewEngine(server.URL, server.Client())
			config, err := engine.Discover(context.Background(), "self", "/data", "/projects", "/projects", "/var/run/docker.sock", "services", "services")
			if err != nil {
				t.Fatalf("host-folder deployment rejected: %v", err)
			}
			if err := engine.Launch(context.Background(), config, Operation{ID: "compose-test", Worker: "services-worker-test"}); err != nil {
				t.Fatal(err)
			}
			mounts := payload["HostConfig"].(map[string]any)["Mounts"].([]any)
			data := mounts[0].(map[string]any)
			if data["Type"] != "bind" || data["Source"] != source || data["Target"] != "/data" || data["ReadOnly"] != false {
				t.Fatalf("worker does not share the exact writable daemon mount: %+v", data)
			}
		})
	}
}
