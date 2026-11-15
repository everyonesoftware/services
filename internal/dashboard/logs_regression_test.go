package dashboard

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"services/internal/dockerapi"
	"services/internal/groups"
)

func TestLogsFirstOutputFromClockBehindEngine(t *testing.T) {
	f := &logFixture{rows: map[string][]dockerapi.LogRecord{}}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	first := requestLogs(t, s, "project=shop")
	f.rows["a"] = []dockerapi.LogRecord{{Timestamp: time.Now().Add(-time.Minute), Stream: "stdout", Message: "first output from lagging Engine"}}
	next := requestLogs(t, s, "project=shop&after="+first.NextAfter)
	if len(next.Entries) != 1 {
		t.Fatalf("lost first output: entries=%d hasNewer=%v warnings=%v", len(next.Entries), next.HasNewer, next.Warnings)
	}
}

func TestLogsRecreationKeepsCursorBounded(t *testing.T) {
	ts := time.Now().Add(-time.Hour)
	f := &logFixture{rows: map[string][]dockerapi.LogRecord{}}
	s := New(fakeSource{}, 1)
	s.SetLogSource(f)
	token := ""
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("%064x", i)
		s.SetActionSource(fakeSource{containers: []groups.Container{{ID: id, Project: "shop"}}})
		f.rows = map[string][]dockerapi.LogRecord{id: {{Timestamp: ts.Add(time.Duration(i) * time.Second), Stream: "stdout", Message: "new instance"}}}
		query := "project=shop"
		if token != "" {
			query += "&after=" + token
		}
		out := requestLogs(t, s, query)
		token = out.NextAfter
		if len(token) > 2048 {
			t.Fatalf("cursor grows with retired instances at %d: %d bytes", i, len(token))
		}
	}
}

func TestLogsRotationInvalidatesTimestampOrdinals(t *testing.T) {
	ts := time.Now().Add(-time.Minute)
	f := &logFixture{rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: ts, Stream: "stdout", Message: "A"}, {Timestamp: ts, Stream: "stdout", Message: "B"}}}}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	first := requestLogs(t, s, "project=shop")
	f.rows["a"] = []dockerapi.LogRecord{{Timestamp: ts, Stream: "stdout", Message: "B"}, {Timestamp: ts, Stream: "stdout", Message: "C new"}}
	for _, query := range []string{"after=" + first.NextAfter, "cursor=" + first.NextCursor} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/api/logs?project=shop&"+query, nil))
		if w.Code != 409 {
			t.Fatalf("rotated boundary must require reset, got %d %s", w.Code, w.Body.String())
		}
	}
}
