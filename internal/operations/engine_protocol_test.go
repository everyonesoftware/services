package operations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateWorkerImageRequiresExactProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		ok         bool
	}{
		{"compatible", `{"Config":{"Labels":{"io.services.worker.protocol":"1"}}}`, 200, true},
		{"missing label", `{"Config":{"Labels":{}}}`, 200, false},
		{"missing config", `{}`, 200, false},
		{"old protocol", `{"Config":{"Labels":{"io.services.worker.protocol":"0"}}}`, 200, false},
		{"future protocol", `{"Config":{"Labels":{"io.services.worker.protocol":"2"}}}`, 200, false},
		{"missing image", `{"message":"not found"}`, 404, false},
		{"engine error", `{"message":"unavailable"}`, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/images/registry.example/services:target/json" {
					t.Errorf("unexpected image inspection: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			validator, ok := any(NewEngine(server.URL, server.Client())).(interface {
				ValidateWorkerImage(context.Context, string) error
			})
			if !ok {
				t.Fatal("Engine cannot validate target worker protocol")
			}
			err := validator.ValidateWorkerImage(context.Background(), "registry.example/services:target")
			if (err == nil) != tc.ok {
				t.Fatalf("protocol validation: %v, want success %v", err, tc.ok)
			}
		})
	}
}
