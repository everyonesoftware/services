package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSelfReplacementRequiresExactImageAndReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, image, id string
		exit            int
		ok              bool
	}{{"ready", "sha256:new", "replacement", 0, true}, {"wrong image", "sha256:old", "replacement", 0, false}, {"same container", "sha256:new", "original", 0, false}, {"unready", "sha256:new", "replacement", 1, false}} {
		t.Run(tc.name, func(t *testing.T) {
			readyCalls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/containers/services/json":
					json.NewEncoder(w).Encode(map[string]any{"Id": tc.id, "Image": tc.image, "State": map[string]any{"Running": true}})
				case r.Method == "POST" && r.URL.Path == "/containers/replacement/exec":
					readyCalls++
					w.Write([]byte(`{"Id":"readiness"}`))
				case r.Method == "POST" && r.URL.Path == "/exec/readiness/start":
					w.Write([]byte(`ready`))
				case r.Method == "GET" && r.URL.Path == "/exec/readiness/json":
					json.NewEncoder(w).Encode(map[string]any{"ExitCode": tc.exit})
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer s.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := NewEngine(s.URL, s.Client()).Verify(ctx, "original", "services", "sha256:new", true)
			if (err == nil) != tc.ok {
				t.Fatalf("verification %v", err)
			}
			if tc.ok && readyCalls != 1 {
				t.Fatal("no independent readiness probe")
			}
		})
	}
}
func TestDiscoverRejectsUnverifiedIdentityOrMissingVolume(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Id":"self","Image":"sha256:exact","Config":{"Labels":{"com.docker.compose.project":"services","com.docker.compose.service":"services"}},"State":{"Running":true},"Mounts":[]}`))
	}))
	defer s.Close()
	if _, err := NewEngine(s.URL, s.Client()).Discover(context.Background(), "self", "/data", "C:/Code", "/projects", "/var/run/docker.sock", "services", "services"); err == nil {
		t.Fatal("accepted missing durable mounts")
	}
}
