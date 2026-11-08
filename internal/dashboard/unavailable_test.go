package dashboard

import (
	"net/http/httptest"
	"testing"
)

func TestUnavailableJournalIsNotPresentedAsEmptyQueue(t *testing.T) {
	s := New(nil, 1)
	s.SetDurableOperations(nil, "services", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/operations", nil))
	if w.Code != 503 {
		t.Fatalf("unavailable journal presented as empty queue: %d %s", w.Code, w.Body.String())
	}
}
