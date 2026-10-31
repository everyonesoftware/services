package dashboard

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"services/internal/groups"
)

type fakeSource struct{ containers []groups.Container }

func (f fakeSource) Containers() ([]groups.Container, error) { return f.containers, nil }

func TestProjectsEndpointReturnsComposeGroupsAndStatus(t *testing.T) {
	server := New(fakeSource{containers: []groups.Container{
		{ID: "abc", Name: "api-1", Project: "store", Service: "api", State: "running", Health: "healthy"},
		{ID: "def", Name: "db-1", Project: "store", Service: "db", State: "exited"},
	}})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/projects", nil))

	if response.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Projects []groups.Project `json:"projects"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Projects) != 1 || body.Projects[0].Name != "store" || body.Projects[0].Status != groups.StatusUnhealthy {
		t.Fatalf("projects = %+v, want one unhealthy store project", body.Projects)
	}
}
