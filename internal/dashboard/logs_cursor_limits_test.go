package dashboard

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"services/internal/dockerapi"
	"services/internal/groups"
)

func TestLogsCursorSizeIsBoundedBeforeResponse(t *testing.T) {
	project := strings.Repeat("p", 33000)
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: project}}}, 1)
	s.SetLogSource(&logFixture{rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: time.Now(), Stream: "stdout", Message: "one"}}}})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/logs?project="+project, nil))
	if w.Code != 503 {
		t.Fatalf("must not issue cursor larger than accepted limit: status=%d", w.Code)
	}
}

func TestLogsMaximumCurrentSourcesRoundTrip(t *testing.T) {
	f := &logFixture{rows: map[string][]dockerapi.LogRecord{}}
	var containers []groups.Container
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("%064x", i)
		containers = append(containers, groups.Container{ID: id, Project: "shop"})
		f.rows[id] = []dockerapi.LogRecord{{Timestamp: time.Unix(1760000000, 123456789), Stream: "stdout", Message: "one"}}
	}
	s := New(fakeSource{containers: containers}, 1)
	s.SetLogSource(f)
	page := requestLogs(t, s, "project=shop")
	if len(page.NextAfter) > 32768 {
		t.Fatal("current-source cursor exceeds accepted limit")
	}
	next := requestLogs(t, s, "project=shop&after="+page.NextAfter)
	if len(next.Entries) != 0 || next.HasNewer {
		t.Fatal("maximum-source cursor did not round trip")
	}
}
