package dockerapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInspectionMonitoringFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			w.Write([]byte(`{"ApiVersion":"1.47"}`))
		case "/v1.47/containers/json":
			w.Write([]byte(`[{"Id":"job","Names":["/job"],"Labels":{"com.docker.compose.oneoff":"True"}}]`))
		case "/v1.47/images/json":
			w.Write([]byte(`[]`))
		case "/v1.47/containers/job/json":
			w.Write([]byte(`{"RestartCount":4,"State":{"Status":"exited","StartedAt":"2026-11-07T12:00:00Z","ExitCode":137,"OOMKilled":true,"Health":{"Status":"unhealthy","Log":[{"Output":"old"},{"Output":"last probe"}]}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	containers, err := New(server.URL, server.Client()).Containers()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(containers[0])
	var got map[string]any
	json.Unmarshal(encoded, &got)
	for key, want := range map[string]any{"restartCount": float64(4), "startedAt": "2026-11-07T12:00:00Z", "exitCode": float64(137), "oomKilled": true, "healthOutput": "last probe", "oneShot": true} {
		if got[key] != want {
			t.Errorf("%s = %v, want %v", key, got[key], want)
		}
	}
}
