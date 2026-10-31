package dashboard

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"services/internal/groups"
)

type unavailableSource struct{}

func (unavailableSource) Containers() ([]groups.Container, error) {
	return nil, errors.New("engine unavailable")
}

func TestVersionEndpointWorksWhenDockerEngineIsUnavailable(t *testing.T) {
	server := New(unavailableSource{}, 42)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/version", nil))

	if response.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Version != 42 {
		t.Fatalf("version = %d, want 42", body.Version)
	}
}
