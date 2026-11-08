package dashboard

import (
	"encoding/json"
	"net/http/httptest"
	"services/internal/monitor"
	"testing"
)

type monitoredTestSource struct{ fakeSource }

func (monitoredTestSource) Snapshot() monitor.Snapshot {
	return monitor.Snapshot{Connected: false, Error: "Docker engine unavailable"}
}
func TestMonitorEndpointReportsConnectivityWithoutPretendingEmptyIsHealthy(t *testing.T) {
	s := New(monitoredTestSource{}, 0)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/monitor", nil))
	var v map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v["connected"] != false || v["error"] != "Docker engine unavailable" {
		t.Fatalf("monitor response %d %s", w.Code, w.Body.String())
	}
}
