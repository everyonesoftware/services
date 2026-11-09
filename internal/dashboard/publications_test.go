package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicationEndpointDisabledByDefault(t *testing.T) {
	server := New(fakeSource{}, 1)
	request := httptest.NewRequest(http.MethodPost, "http://localhost/api/images/published", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want disabled endpoint 503; body = %s", response.Code, response.Body.String())
	}
}
