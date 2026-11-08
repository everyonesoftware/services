package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"services/internal/groups"
)

func listOperations(t *testing.T, s *Server) []operationResponse {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/operations", nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("activity response=%d %s", w.Code, w.Body.String())
	}
	var body struct {
		Operations []operationResponse `json:"operations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Operations == nil {
		t.Fatal("operations must be a JSON array")
	}
	return body.Operations
}

func TestOperationQueueDeduplicatesConcurrentTabsAndRunsFIFOAfterFailure(t *testing.T) {
	started := make(chan struct{})
	finish := make(chan struct{})
	var finishOnce sync.Once
	unblock := func() { finishOnce.Do(func() { close(finish) }) }
	var mu sync.Mutex
	var order []string
	makeServer := func(name string, block bool) *Server {
		return projectTestServer([]groups.Container{{Project: name, UpdateAvailable: true}}, &projectUpdaterStub{run: func(context.Context) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			if block {
				close(started)
				<-finish
				return errors.New("first failed")
			}
			return nil
		}})
	}
	firstServer := makeServer("queue-first", true)
	first := submitProject(t, firstServer, "queue-first")
	<-started
	var queued []operationResponse
	defer func() {
		unblock()
		awaitOperation(t, firstServer, first.ID)
		for _, op := range queued {
			awaitOperation(t, firstServer, op.ID)
		}
	}()
	// Different Server instances model tabs and retain each request's own updater.
	secondServer := makeServer("queue-second", false)
	second := submitProject(t, secondServer, "queue-second")
	queued = append(queued, second)
	thirdServer := makeServer("queue-third", false)
	third := submitProject(t, thirdServer, "queue-third")
	queued = append(queued, third)
	if second.QueuePosition != 1 || third.QueuePosition != 2 {
		t.Fatalf("positions=%+v %+v", second, third)
	}
	var wg sync.WaitGroup
	responses := make(chan operationResponse, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			secondServer.ServeHTTP(w, projectRequest("queue-second"))
			var op operationResponse
			if w.Code == 202 {
				_ = json.Unmarshal(w.Body.Bytes(), &op)
			}
			responses <- op
		}()
	}
	wg.Wait()
	close(responses)
	for op := range responses {
		if op.ID != second.ID {
			t.Errorf("duplicate queued operation got ID=%q want %q", op.ID, second.ID)
		}
	}
	duplicate := submitProject(t, thirdServer, "queue-first")
	if duplicate.ID != first.ID {
		t.Errorf("running duplicate=%s want %s", duplicate.ID, first.ID)
	}
	operations := listOperations(t, thirdServer)
	if len(operations) < 3 || operations[0].ID != third.ID || operations[1].ID != second.ID || operations[2].ID != first.ID {
		t.Errorf("not newest submitted first: %+v", operations)
	}
	// Observe terminal state and ordering after failure before deferred cleanup.
	unblock()
	if op := awaitOperation(t, firstServer, first.ID); op.State != "error" {
		t.Errorf("first=%+v", op)
	}
	awaitOperation(t, firstServer, second.ID)
	awaitOperation(t, firstServer, third.ID)
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(order, []string{"queue-first", "queue-second", "queue-third"}) {
		t.Errorf("execution order=%v", order)
	}
}

func TestOperationQueueBoundsWaitingAndStillDeduplicates(t *testing.T) {
	s := projectTestServer(nil, &projectUpdaterStub{})
	release, ok := s.tryComposeAction("")
	if !ok {
		t.Fatal("queue busy")
	}
	var accepted []operationResponse
	defer func() {
		release()
		for _, op := range accepted {
			awaitOperation(t, s, op.ID)
		}
	}()
	for i := 0; i < 32; i++ {
		accepted = append(accepted, submitProject(t, s, fmt.Sprintf("bounded-%d", i)))
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, projectRequest("overflow"))
	if w.Code != 429 {
		t.Errorf("overflow code=%d want 429", w.Code)
	}
	duplicate := submitProject(t, s, "bounded-0")
	if duplicate.ID != accepted[0].ID {
		t.Errorf("full queue duplicate created work: %+v", duplicate)
	}
}

func TestQueuedProjectPreflightUsesInventoryAndIdentityAtExecution(t *testing.T) {
	for _, tc := range []string{"inventory", "identity", "engine unavailable"} {
		t.Run(tc, func(t *testing.T) {
			updater := &projectUpdaterStub{}
			s := projectTestServer([]groups.Container{{Project: "preflight", UpdateAvailable: true}}, updater)
			release, ok := s.tryComposeAction("")
			if !ok {
				t.Fatal("queue busy")
			}
			defer release()
			op := submitProject(t, s, "preflight")
			switch tc {
			case "inventory":
				s.SetActionSource(fakeSource{})
			case "identity":
				t.Setenv("SERVICES_PROJECT_NAME", "preflight")
			case "engine unavailable":
				s.SetActionSource(failingActionSource{})
			}
			release()
			op = awaitOperation(t, s, op.ID)
			if op.State != "error" || op.Error == "" || len(updater.members) != 0 {
				t.Fatalf("stale preflight: %+v members=%v", op, updater.members)
			}
		})
	}
}

type failingActionSource struct{}

func (failingActionSource) Containers() ([]groups.Container, error) {
	return nil, errors.New("unavailable")
}

type blockingLegacyUpdater struct {
	fakeServiceUpdater
	started chan context.Context
	finish  chan struct{}
}

func (u *blockingLegacyUpdater) Pull(ctx context.Context, _ groups.Container) error {
	u.started <- ctx
	<-u.finish
	return errors.New("legacy pull failed")
}

func TestLegacyStreamIsVisibleAndCannotBypassQueuedProject(t *testing.T) {
	u := &blockingLegacyUpdater{started: make(chan context.Context, 1), finish: make(chan struct{})}
	s := NewWithActionsAndAllowedHosts(fakeSource{containers: []groups.Container{{ID: "legacy", Project: "legacy-project", State: "running", UpdateAvailable: true}}}, nil, u, 0, []string{"dashboard.test"})
	w := httptest.NewRecorder()
	done := make(chan struct{})
	r := httptest.NewRequest("POST", "http://dashboard.test/api/containers/legacy/pull", nil)
	r.Header.Set("Origin", "http://dashboard.test")
	requestCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() { defer close(done); s.ServeHTTP(w, r.WithContext(requestCtx)) }()
	ctx := <-u.started
	cancel()
	var pending operationResponse
	var finishOnce sync.Once
	unblock := func() { finishOnce.Do(func() { close(u.finish) }); <-done }
	defer func() {
		unblock()
		if pending.ID != "" {
			awaitOperation(t, s, pending.ID)
		}
	}()
	if ctx.Err() != nil {
		t.Fatal("legacy work canceled by browser")
	}
	operations := listOperations(t, s)
	active := operations[0]
	if active.Project != "legacy-project" || active.Action != "pull" || active.State != "running" || !strings.Contains(strings.Join(active.Messages, "\n"), "Starting Compose pull") {
		t.Fatalf("legacy not visible: %+v", active)
	}
	other := projectTestServer([]groups.Container{{Project: "behind-legacy", UpdateAvailable: true}}, &projectUpdaterStub{})
	pending = submitProject(t, other, "behind-legacy")
	if pending.State != "queued" {
		t.Fatalf("project bypassed legacy: %+v", pending)
	}
	rejected := httptest.NewRecorder()
	other.ServeHTTP(rejected, func() *http.Request {
		req := httptest.NewRequest("POST", "http://dashboard.test/api/containers/legacy/update", nil)
		req.Header.Set("Origin", "http://dashboard.test")
		return req
	}())
	if rejected.Code != 409 {
		t.Fatalf("legacy bypassed queue: %d", rejected.Code)
	}
	unblock()
	terminal := awaitOperation(t, s, active.ID)
	if terminal.State != "error" || terminal.Error != "legacy pull failed" || terminal.FinishedAt == "" {
		t.Fatalf("legacy terminal=%+v", terminal)
	}
	if !strings.Contains(w.Body.String(), `"type":"error"`) {
		t.Fatalf("legacy stream lost terminal: %s", w.Body.String())
	}
	awaitOperation(t, s, pending.ID)
}

func awaitQueueJob(t *testing.T, q *operationQueue, id string) Operation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		q.mu.Lock()
		var op Operation
		for _, job := range q.history {
			if job.operation.ID == id {
				op = q.snapshotLocked(job)
				break
			}
		}
		q.mu.Unlock()
		if op.State == "complete" || op.State == "error" {
			return op
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("queue job never finished")
	return Operation{}
}

func TestOperationQueueBoundsOutputAndTerminalHistory(t *testing.T) {
	q := &operationQueue{}
	noisy, ok := q.submit("noisy", func(_ context.Context, progress func(string)) error {
		for i := 0; i < 250; i++ {
			progress(fmt.Sprintf("%d:%s", i, strings.Repeat("x", 8000)))
		}
		return errors.New(strings.Repeat("e", 8000))
	})
	if !ok {
		t.Fatal("submit rejected")
	}
	noisy = awaitQueueJob(t, q, noisy.ID)
	if len(noisy.Messages) != 200 || len(noisy.Error) > 4096 {
		t.Errorf("unbounded output messages=%d error bytes=%d", len(noisy.Messages), len(noisy.Error))
	}
	for _, message := range noisy.Messages {
		if len(message) > 4096 {
			t.Errorf("unbounded message bytes=%d", len(message))
			break
		}
	}
	if !strings.HasPrefix(noisy.Messages[len(noisy.Messages)-1], "249:") {
		t.Error("newest output lost")
	}
	for i := 0; i < 105; i++ {
		op, ok := q.submit("history", func(context.Context, func(string)) error { return nil })
		if !ok {
			t.Fatal("submit rejected")
		}
		awaitQueueJob(t, q, op.ID)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.history) != 100 {
		t.Errorf("terminal history=%d want 100", len(q.history))
	}
	for _, job := range q.history {
		if job.operation.ID == noisy.ID {
			t.Error("old terminal operation not evicted")
		}
		if job.run != nil {
			t.Error("completed history retains server closure")
		}
	}
}

func TestUnknownOperationReturnsNotFound(t *testing.T) {
	s := projectTestServer(nil, &projectUpdaterStub{})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/api/operations/no-such-operation", nil))
	if w.Code != 404 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}
