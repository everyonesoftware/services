package dashboard

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"services/internal/dockerapi"
	"services/internal/groups"
)

type logFixture struct {
	rows             map[string][]dockerapi.LogRecord
	cap              int
	errors           map[string]error
	tailBeforeBounds bool
}

func (f *logFixture) Logs(ctx context.Context, id string, opts dockerapi.LogOptions) ([]dockerapi.LogRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := f.errors[id]; err != nil {
		return nil, err
	}
	var out []dockerapi.LogRecord
	input := f.rows[id]
	if f.tailBeforeBounds && opts.Tail > 0 && len(input) > opts.Tail {
		input = input[len(input)-opts.Tail:]
	}
	for _, row := range input {
		if (!opts.Since.IsZero() && row.Timestamp.Before(opts.Since)) || (!opts.Until.IsZero() && !row.Timestamp.Before(opts.Until)) {
			continue
		}
		out = append(out, row)
	}
	if opts.Tail > 0 && len(out) > opts.Tail {
		out = out[len(out)-opts.Tail:]
	}
	if f.cap > 0 && len(out) > f.cap {
		return nil, dockerapi.ErrLogLimit
	}
	return out, nil
}
func requestLogs(t *testing.T, s *Server, query string) LogsResponse {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/logs?"+query, nil))
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	var out LogsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestLogsHistoryPreservesTimestampTiesAndDuplicateMessages(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := &logFixture{rows: map[string][]dockerapi.LogRecord{"a": {}}}
	for i := 0; i < 9; i++ {
		f.rows["a"] = append(f.rows["a"], dockerapi.LogRecord{Timestamp: ts, Stream: "stdout", Message: "same"})
	}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	out := requestLogs(t, s, "project=shop&limit=2")
	seen := map[string]bool{}
	pages := 0
	for {
		pages++
		for _, e := range out.Entries {
			if seen[e.ID] {
				t.Fatalf("duplicate id %s", e.ID)
			}
			seen[e.ID] = true
		}
		if !out.HasMore {
			break
		}
		if pages > 6 {
			t.Fatal("history does not advance")
		}
		out = requestLogs(t, s, "project=shop&limit=2&cursor="+out.NextCursor)
	}
	if len(seen) != 9 {
		t.Fatalf("delivered %d of 9", len(seen))
	}
}

func TestLogsAfterDrainsOldestUnreadWithoutDroppingTies(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := &logFixture{rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: ts, Stream: "stdout", Message: "initial"}}}}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}, {ID: "b", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	initial := requestLogs(t, s, "project=shop&limit=2")
	for i := 0; i < 7; i++ {
		id := "a"
		if i%2 == 0 {
			id = "b"
		}
		f.rows[id] = append(f.rows[id], dockerapi.LogRecord{Timestamp: ts, Stream: "stderr", Message: "same"})
	}
	cursor := initial.NextAfter
	seen := map[string]bool{initial.Entries[0].ID: true}
	total := 0
	for i := 0; i < 8; i++ {
		out := requestLogs(t, s, "project=shop&limit=2&after="+cursor)
		for _, e := range out.Entries {
			if seen[e.ID] {
				t.Fatalf("redelivered %s", e.ID)
			}
			seen[e.ID] = true
			total++
		}
		if out.NextAfter == "" {
			t.Fatal("missing incremental cursor")
		}
		cursor = out.NextAfter
		if !out.HasNewer {
			break
		}
	}
	if total != 7 {
		t.Fatalf("got %d unread, want 7", total)
	}
	empty := requestLogs(t, s, "project=shop&limit=2&after="+cursor)
	if len(empty.Entries) != 0 || empty.HasNewer || empty.NextAfter != cursor {
		t.Fatalf("empty poll=%+v", empty)
	}
}

func TestLogsRejectInvalidOrCrossScopeCursors(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}, {ID: "b", Project: "other"}}}, 1)
	s.SetLogSource(&logFixture{rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: ts, Stream: "stdout"}}}})
	token := requestLogs(t, s, "project=shop").NextAfter
	// A client cannot manufacture even an otherwise valid cursor.
	forged, _ := json.Marshal(logCursor{Project: "shop", Position: logPosition{Timestamp: ts, ContainerID: "a", Ordinal: 0}})
	for _, query := range []string{
		"project=shop&cursor=" + base64.RawURLEncoding.EncodeToString(forged),
		"project=other&cursor=" + token, "project=shop&container=a&cursor=" + token,
		"project=shop&cursor=" + token + "&after=" + token, "project=shop&cursor=bad",
		"project=shop&limit=0", "project=shop&limit=501", "project=shop&project=other",
	} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/api/logs?"+query, nil))
		if w.Code != 400 {
			t.Errorf("%s => %d", query, w.Code)
		}
	}
	if strings.Contains(token, "same") {
		t.Fatal("cursor contains message")
	}
}

func TestLogsBusyAfterShrinksWindowWithoutSkippingAcrossContainers(t *testing.T) {
	ts := time.Now().UTC().Add(-time.Minute)
	f := &logFixture{cap: 5, rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: ts, Stream: "stdout", Message: "initial"}}}}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}, {ID: "b", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	cursor := requestLogs(t, s, "project=shop&limit=2").NextAfter
	for i := 1; i <= 30; i++ {
		id := "a"
		if i%2 == 0 {
			id = "b"
		}
		f.rows[id] = append(f.rows[id], dockerapi.LogRecord{Timestamp: ts.Add(time.Duration(i) * time.Second), Stream: "stdout", Message: "unread"})
	}
	seen := map[string]bool{}
	var previous time.Time
	for i := 0; i < 80; i++ {
		out := requestLogs(t, s, "project=shop&limit=2&after="+cursor)
		for _, e := range out.Entries {
			if seen[e.ID] || e.Timestamp.Before(previous) {
				t.Fatalf("skip/order violation %+v", e)
			}
			seen[e.ID] = true
			previous = e.Timestamp
		}
		if out.NextAfter == "" {
			t.Fatal("missing progress")
		}
		cursor = out.NextAfter
		if !out.HasNewer {
			break
		}
	}
	if len(seen) != 30 {
		t.Fatalf("delivered %d of 30", len(seen))
	}
}

func TestLogsAfterAcceptsNewTiedRecordFromEarlierContainer(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := &logFixture{rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: ts, Stream: "stdout", Message: "old-a"}}, "b": {{Timestamp: ts, Stream: "stdout", Message: "old-b"}}}}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}, {ID: "b", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	token := requestLogs(t, s, "project=shop&limit=1").NextAfter
	f.rows["a"] = append(f.rows["a"], dockerapi.LogRecord{Timestamp: ts, Stream: "stdout", Message: "new-a"})
	out := requestLogs(t, s, "project=shop&after="+token)
	if len(out.Entries) != 1 || out.Entries[0].Message != "new-a" {
		t.Fatalf("new tied record skipped: %+v", out)
	}
}

func TestLogsEmptyInitialCanFollow(t *testing.T) {
	f := &logFixture{rows: map[string][]dockerapi.LogRecord{}}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	out := requestLogs(t, s, "project=shop")
	if out.NextAfter == "" || out.Entries == nil || out.HasMore {
		t.Fatalf("empty=%+v", out)
	}
	f.rows["a"] = []dockerapi.LogRecord{{Timestamp: time.Now().UTC(), Stream: "stdout", Message: "arrived"}}
	next := requestLogs(t, s, "project=shop&after="+out.NextAfter)
	if len(next.Entries) != 1 {
		t.Fatalf("follow=%+v", next)
	}
}
func TestLogsPartialFailuresWarnWithoutAdvancingCursors(t *testing.T) {
	f := &logFixture{rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: time.Now().Add(-time.Second), Stream: "stdout", Message: "ok"}}}, errors: map[string]error{}}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}, {ID: "b", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	token := requestLogs(t, s, "project=shop").NextAfter
	f.errors["b"] = errors.New("secret engine log snippet")
	f.rows["a"] = append(f.rows["a"], dockerapi.LogRecord{Timestamp: time.Now(), Stream: "stdout", Message: "new"})
	out := requestLogs(t, s, "project=shop&after="+token)
	if len(out.Entries) != 1 || len(out.Warnings) != 1 || out.NextAfter != token || out.NextCursor != "" || !out.HasNewer || strings.Contains(out.Warnings[0].Message, "secret") {
		t.Fatalf("partial=%+v", out)
	}
}

type logSourceFunc func(context.Context, string, dockerapi.LogOptions) ([]dockerapi.LogRecord, error)

func (f logSourceFunc) Logs(ctx context.Context, id string, opts dockerapi.LogOptions) ([]dockerapi.LogRecord, error) {
	return f(ctx, id, opts)
}
func TestLogsBoundsConcurrentReadsAndCancels(t *testing.T) {
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	entered := make(chan struct{}, 5)
	var active atomic.Int32
	s.SetLogSource(logSourceFunc(func(ctx context.Context, _ string, _ dockerapi.LogOptions) ([]dockerapi.LogRecord, error) {
		active.Add(1)
		defer active.Add(-1)
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/logs?project=shop", nil).WithContext(ctx))
		}()
	}
	for i := 0; i < 4; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("read not started")
		}
	}
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/logs?project=shop", nil).WithContext(short))
	if w.Code != 429 {
		t.Errorf("saturated logs=%d, want 429", w.Code)
	}
	cancel()
	wg.Wait()
	if active.Load() != 0 {
		t.Fatal("canceled reads leaked")
	}
}

func TestLogsRejectWrongCursorDirection(t *testing.T) {
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	s.SetLogSource(&logFixture{rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: time.Now().Add(-time.Second), Stream: "stdout"}}}})
	page := requestLogs(t, s, "project=shop")
	for _, query := range []string{"project=shop&after=" + page.NextCursor, "project=shop&cursor=" + page.NextAfter, "project=shop&cursor="} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/api/logs?"+query, nil))
		if w.Code != 400 {
			t.Errorf("invalid direction/empty cursor status=%d", w.Code)
		}
	}
}

func TestLogsHistoryWorksWhenDockerTailsBeforeUntilFiltering(t *testing.T) {
	ts := time.Now().Add(-time.Hour)
	f := &logFixture{tailBeforeBounds: true, cap: 10, rows: map[string][]dockerapi.LogRecord{}}
	for i := 0; i < 30; i++ {
		f.rows["a"] = append(f.rows["a"], dockerapi.LogRecord{Timestamp: ts.Add(time.Duration(i) * time.Second), Stream: "stdout", Message: "line"})
	}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	page := requestLogs(t, s, "project=shop&limit=2")
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		for _, e := range page.Entries {
			if seen[e.ID] {
				t.Fatal("duplicate history")
			}
			seen[e.ID] = true
		}
		if !page.HasMore {
			break
		}
		page = requestLogs(t, s, "project=shop&limit=2&cursor="+page.NextCursor)
	}
	if len(seen) != 30 {
		t.Fatalf("Docker tail/until skipped history: delivered %d of 30", len(seen))
	}
}

func TestLogsRecentHandlesEngineClockAhead(t *testing.T) {
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	s.SetLogSource(&logFixture{tailBeforeBounds: true, rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: time.Now().Add(time.Hour), Stream: "stdout", Message: "future engine clock"}}}})
	out := requestLogs(t, s, "project=shop")
	if len(out.Entries) != 1 {
		t.Fatalf("recent output hidden by clock skew: %+v", out)
	}
}

func TestLogsFollowingHandlesEngineClockAhead(t *testing.T) {
	ts := time.Now().Add(time.Hour)
	f := &logFixture{rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: ts, Stream: "stdout", Message: "initial"}}}}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	token := requestLogs(t, s, "project=shop").NextAfter
	f.rows["a"] = append(f.rows["a"], dockerapi.LogRecord{Timestamp: ts.Add(time.Second), Stream: "stdout", Message: "new"})
	out := requestLogs(t, s, "project=shop&after="+token)
	if len(out.Entries) != 1 || out.Entries[0].Message != "new" {
		t.Fatalf("future live output hidden: %+v", out)
	}
}

func TestLogsHistoryDoesNotCrossAnIncompleteContainerWindow(t *testing.T) {
	ts := time.Now().Add(-time.Hour)
	f := &logFixture{cap: 10, rows: map[string][]dockerapi.LogRecord{"b": {{Timestamp: ts.Add(-24 * time.Hour), Stream: "stdout", Message: "old b"}}}}
	for i := 0; i < 30; i++ {
		f.rows["a"] = append(f.rows["a"], dockerapi.LogRecord{Timestamp: ts.Add(time.Duration(i) * time.Second), Stream: "stdout", Message: "a"})
	}
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Project: "shop"}, {ID: "b", Project: "shop"}}}, 1)
	s.SetLogSource(f)
	page := requestLogs(t, s, "project=shop&limit=2")
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		for _, e := range page.Entries {
			if seen[e.ID] {
				t.Fatal("duplicate history")
			}
			seen[e.ID] = true
		}
		if !page.HasMore {
			break
		}
		page = requestLogs(t, s, "project=shop&limit=200&cursor="+page.NextCursor)
	}
	if len(seen) != 31 {
		t.Fatalf("history crossed incomplete source: delivered %d of 31", len(seen))
	}
}

func TestLogsRecentProjectPage(t *testing.T) {
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := New(fakeSource{containers: []groups.Container{{ID: "a", Name: "api", Project: "shop"}, {ID: "b", Name: "db", Project: "shop"}, {ID: "x", Project: "other"}}}, 1)
	s.SetLogSource(&logFixture{rows: map[string][]dockerapi.LogRecord{"a": {{Timestamp: ts, Stream: "stdout", Message: "first"}, {Timestamp: ts.Add(time.Second), Stream: "stdout", Message: "last"}}, "b": {{Timestamp: ts, Stream: "stderr", Message: "db"}}}})
	out := requestLogs(t, s, "project=shop&limit=2")
	if len(out.Entries) != 2 || out.Entries[0].ContainerID != "b" || out.Entries[1].Message != "last" || !out.HasMore || out.NextCursor == "" || out.NextAfter == "" {
		t.Fatalf("out=%+v", out)
	}
}
