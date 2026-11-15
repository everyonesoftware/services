package dashboard

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"services/internal/dockerapi"
	"services/internal/groups"
)

func TestLogsStandaloneUsesOnlyEmptyComposeProjectAndLiveInventory(t *testing.T) {
	s := New(fakeSource{containers: []groups.Container{{ID: "stale", Project: ""}}}, 1)
	s.SetActionSource(fakeSource{containers: []groups.Container{{ID: "solo", Project: ""}, {ID: "impostor", Project: "Standalone"}, {ID: "api", Project: "shop"}}})
	s.SetLogSource(logSourceFunc(func(_ context.Context, id string, _ dockerapi.LogOptions) ([]dockerapi.LogRecord, error) {
		if id != "solo" {
			t.Errorf("read unselected %q", id)
		}
		return []dockerapi.LogRecord{{Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Stream: "stdout", Message: "standalone"}}, nil
	}))
	out := requestLogs(t, s, "project=Standalone")
	if len(out.Entries) != 1 || out.Entries[0].ContainerID != "solo" {
		t.Fatalf("standalone=%+v", out)
	}
	for _, query := range []string{"project=Standalone&container=stale", "project=Standalone&container=impostor", "project=shop&container=solo", "project=shop&container=ap", "project=missing"} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/api/logs?"+query, nil))
		if w.Code != 404 {
			t.Errorf("%s = %d", query, w.Code)
		}
	}
}

// A real HTTP Engine fixture, not a live Docker verification.
func TestLogsSessionToEngineIntegrationAndReadKeyRejection(t *testing.T) {
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.51"}`)
		case "/v1.51/containers/abc/json":
			fmt.Fprint(w, `{"Config":{"Tty":false}}`)
		case "/v1.51/containers/abc/logs":
			text := "2026-01-01T00:00:00.123456789Z <script>untrusted</script>\n"
			frame := make([]byte, 8)
			frame[0] = 2
			binary.BigEndian.PutUint32(frame[4:], uint32(len(text)))
			w.Write(append(frame, []byte(text)...))
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	s, store := securedServer(t)
	s.source = fakeSource{containers: []groups.Container{{ID: "abc", Name: "api", Project: "shop"}}}
	s.SetLogSource(dockerapi.New(engine.URL, engine.Client()))
	if _, err := store.Bootstrap("admin", "a sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	_, session, err := store.Login("admin", "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := store.CreateKey("reader", []string{"services:read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if w := authRequest(s, "GET", "/api/logs?project=shop", nil, nil, ""); w.Code != 401 {
		t.Fatalf("anonymous=%d", w.Code)
	}
	if w := authRequest(s, "GET", "/api/logs?project=shop", nil, nil, key); w.Code != 403 {
		t.Fatalf("read key=%d", w.Code)
	}
	w := authRequest(s, "GET", "/api/logs?project=shop", nil, &http.Cookie{Name: sessionCookieName, Value: session}, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"stream":"stderr"`) || !strings.Contains(w.Body.String(), `\u003cscript\u003e`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("session response=%d %s", w.Code, w.Body.String())
	}
}

func TestLogsPartialRecordCompletionIsNotDropped(t *testing.T) {
	stamp := "2026-01-01T00:00:00.000000000Z "
	frame := func(text string) []byte {
		header := make([]byte, 8)
		header[0] = 1
		binary.BigEndian.PutUint32(header[4:], uint32(len(text)))
		return append(header, []byte(text)...)
	}
	body := frame(stamp + strings.Repeat("x", 16<<10))
	var mu sync.RWMutex
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.51"}`)
		case "/v1.51/containers/a/json":
			fmt.Fprint(w, `{"Config":{"Tty":false}}`)
		case "/v1.51/containers/a/logs":
			mu.RLock()
			w.Write(body)
			mu.RUnlock()
		default:
			http.NotFound(w, r)
		}
	}))
	defer engine.Close()
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	s.SetLogSource(dockerapi.New(engine.URL, engine.Client()))
	first := requestLogs(t, s, "project=shop")
	if len(first.Entries) != 1 || len(first.Entries[0].Message) != 16<<10 {
		t.Fatal("initial partial record missing")
	}
	mu.Lock()
	body = append(body, frame(stamp[:12])...)
	body = append(body, frame(stamp[12:]+"end\n")...)
	mu.Unlock()
	next := requestLogs(t, s, "project=shop&after="+first.NextAfter)
	if len(next.Entries) != 1 || next.Entries[0].Message != "end" || next.Entries[0].ID == first.Entries[0].ID {
		t.Fatalf("new partial continuation lost or reused consumed ID: %+v", next)
	}
}
