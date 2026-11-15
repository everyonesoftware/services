package dashboard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"services/internal/dockerapi"
	"services/internal/groups"
)

func TestLogsTTYPartialAmbiguityIsExplicitNotRotation(t *testing.T) {
	stamp := "2026-01-01T00:00:00.000000000Z "
	var body atomic.Value
	body.Store(stamp + "partial")
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.51"}`)
		case "/v1.51/containers/a/json":
			fmt.Fprint(w, `{"Config":{"Tty":true}}`)
		case "/v1.51/containers/a/logs":
			fmt.Fprint(w, body.Load().(string))
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	s.SetLogSource(dockerapi.New(engine.URL, engine.Client()))
	first := requestLogs(t, s, "project=shop")
	body.Store(stamp + "partial" + stamp + "end\n")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/logs?project=shop&after="+first.NextAfter, nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "TTY") {
		t.Fatalf("ambiguous raw output must explain TTY limitation, not claim rotation: status=%d body=%s", w.Code, w.Body.String())
	}
}
