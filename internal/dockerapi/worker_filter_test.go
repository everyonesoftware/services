package dockerapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagedWorkersDoNotAppearAsMonitoredApplications(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			w.Write([]byte(`{"ApiVersion":"1.47"}`))
		case "/v1.47/containers/json":
			w.Write([]byte(`[{"Id":"worker","Names":["/services-worker-test"],"State":"exited","Labels":{"io.services.worker":"true"}}]`))
		case "/v1.47/images/json":
			w.Write([]byte(`[]`))
		case "/v1.47/containers/worker/json":
			w.Write([]byte(`{"State":{"Status":"exited","ExitCode":1}}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	containers, err := New(server.URL, server.Client()).Containers()
	if err != nil || len(containers) != 0 {
		t.Fatalf("helper became a failed application: %+v %v", containers, err)
	}
}
