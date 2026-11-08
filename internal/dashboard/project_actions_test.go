package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"services/internal/groups"
)

type projectUpdaterStub struct {
	fakeServiceUpdater
	members []groups.Container
	run     func(context.Context) error
}

func (f *projectUpdaterStub) UpdateProject(ctx context.Context, members []groups.Container) error {
	f.members = append([]groups.Container(nil), members...)
	if f.run != nil {
		return f.run(ctx)
	}
	return nil
}
func projectRequest(name string) *http.Request {
	r := httptest.NewRequest("POST", "http://dashboard.test/api/projects/"+name+"/update", nil)
	r.Header.Set("Origin", "http://dashboard.test")
	return r
}
func projectTestServer(containers []groups.Container, updater *projectUpdaterStub) *Server {
	s := NewWithActionsAndAllowedHosts(fakeSource{containers: containers}, nil, updater, 42, []string{"dashboard.test"})
	return s
}
func TestProjectUpdateRejectsUntrustedOriginsAndHosts(t *testing.T) {
	for _, tc := range []struct{ name, host, origin string }{
		{"cross origin", "dashboard.test", "http://evil.test"},
		{"untrusted host", "evil.test", "http://evil.test"},
		{"missing origin", "dashboard.test", ""},
		{"wrong scheme", "dashboard.test", "https://dashboard.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updater := &projectUpdaterStub{}
			s := projectTestServer([]groups.Container{{Project: "shop", Service: "web", UpdateAvailable: true}}, updater)
			r := projectRequest("shop")
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 403 || len(updater.members) != 0 {
				t.Fatalf("code=%d members=%v", w.Code, updater.members)
			}
		})
	}
}

func TestProjectUpdateRecordsMissingOrUnchangedProjects(t *testing.T) {
	for _, tc := range []struct {
		name    string
		members []groups.Container
		message string
	}{
		{"unknown", nil, "Project not found"},
		{"shop", []groups.Container{{Project: "shop", Service: "web"}}, "Project has no available image updates"},
		{"standalone", []groups.Container{{Service: "web", UpdateAvailable: true}}, "Project not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updater := &projectUpdaterStub{}
			s := projectTestServer(tc.members, updater)
			op := submitProject(t, s, tc.name)
			op = awaitOperation(t, s, op.ID)
			if op.State != "error" || op.Error != tc.message || len(updater.members) != 0 {
				t.Fatalf("operation=%+v members=%v", op, updater.members)
			}
		})
	}
}
func TestProjectUpdateUsesFreshEngineInventory(t *testing.T) {
	updater := &projectUpdaterStub{}
	s := projectTestServer([]groups.Container{{Project: "shop", Service: "web", UpdateAvailable: true}}, updater)
	s.SetActionSource(fakeSource{})
	op := submitProject(t, s, "shop")
	op = awaitOperation(t, s, op.ID)
	if op.State != "error" || op.Error != "Project not found" || len(updater.members) != 0 {
		t.Fatalf("stale inventory used: %+v", op)
	}
}

func TestProjectUpdateSerializesGloballyUntilDetachedWorkCompletes(t *testing.T) {
	started := make(chan context.Context, 1)
	finish := make(chan struct{})
	updater := &projectUpdaterStub{run: func(ctx context.Context) error { started <- ctx; <-finish; return nil }}
	s := projectTestServer([]groups.Container{{Project: "shop", UpdateAvailable: true}}, updater)
	r := projectRequest("shop")
	requestCtx, cancel := context.WithCancel(r.Context())
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r.WithContext(requestCtx))
	if w.Code != 202 {
		t.Fatalf("code=%d", w.Code)
	}
	var first operationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	ctx := <-started
	cancel()
	var second operationResponse
	defer func() {
		close(finish)
		awaitOperation(t, s, first.ID)
		if second.ID != "" {
			awaitOperation(t, s, second.ID)
		}
	}()
	if ctx.Err() != nil {
		t.Fatal("browser cancellation stopped Compose work")
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 15*time.Minute {
		t.Fatal("missing bounded background timeout")
	}
	otherUpdater := &projectUpdaterStub{}
	other := projectTestServer([]groups.Container{{Project: "other", UpdateAvailable: true}}, otherUpdater)
	second = submitProject(t, other, "other")
	if second.State != "queued" || second.QueuePosition != 1 {
		t.Fatalf("not queued: %+v", second)
	}
}

func TestProjectUpdateRejectsSelfProject(t *testing.T) {
	for _, tc := range []struct {
		config, project string
		code            int
	}{
		{"", "services", 409}, {"control-plane", "control-plane", 409}, {"INVALID", "shop", 503},
	} {
		t.Run(tc.config+tc.project, func(t *testing.T) {
			t.Setenv("SERVICES_PROJECT_NAME", tc.config)
			updater := &projectUpdaterStub{}
			s := projectTestServer([]groups.Container{{Project: tc.project, Service: "web", UpdateAvailable: true}}, updater)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, projectRequest(tc.project))
			if w.Code != tc.code || len(updater.members) != 0 {
				t.Fatalf("code=%d members=%v", w.Code, updater.members)
			}
		})
	}
}

func TestProjectUpdateFailureRecordsTerminalErrorAndContinues(t *testing.T) {
	updater := &projectUpdaterStub{run: func(context.Context) error { return errors.New("pull failed") }}
	s := projectTestServer([]groups.Container{{Project: "shop", UpdateAvailable: true}}, updater)
	op := submitProject(t, s, "shop")
	op = awaitOperation(t, s, op.ID)
	if op.State != "error" || op.Error != "pull failed" || op.FinishedAt == "" {
		t.Fatalf("operation=%+v", op)
	}
	updater.run = nil
	op = submitProject(t, s, "shop")
	if op = awaitOperation(t, s, op.ID); op.State != "complete" {
		t.Fatalf("queue stuck: %+v", op)
	}
}

func TestProjectUpdateAppliesRegistryStatusBeforeSelection(t *testing.T) {
	updater := &projectUpdaterStub{}
	s := projectTestServer([]groups.Container{{Project: "shop", Service: "web"}}, updater)
	s.updates = fakeUpdateApplier{}
	op := submitProject(t, s, "shop")
	op = awaitOperation(t, s, op.ID)
	if op.State != "complete" || len(updater.members) != 1 || !updater.members[0].UpdateAvailable {
		t.Fatalf("operation=%+v members=%v", op, updater.members)
	}
}

type operationResponse struct {
	ID            string   `json:"id"`
	Project       string   `json:"project"`
	Action        string   `json:"action"`
	State         string   `json:"state"`
	QueuedAt      string   `json:"queuedAt"`
	StartedAt     string   `json:"startedAt"`
	FinishedAt    string   `json:"finishedAt"`
	Messages      []string `json:"messages"`
	Error         string   `json:"error"`
	QueuePosition int      `json:"queuePosition"`
}

func submitProject(t *testing.T, s *Server, name string) operationResponse {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, projectRequest(name))
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit code=%d body=%s", w.Code, w.Body.String())
	}
	var op operationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil {
		t.Fatal(err)
	}
	if op.ID == "" || op.Project != name || op.Action != "update project" || op.QueuedAt == "" || op.Messages == nil {
		t.Fatalf("invalid operation: %+v", op)
	}
	return op
}

func awaitOperation(t *testing.T, s *Server, id string) operationResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/api/operations/"+id, nil))
		if w.Code != 200 {
			t.Fatalf("operation lookup: %d %s", w.Code, w.Body.String())
		}
		var op operationResponse
		if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil {
			t.Fatal(err)
		}
		if op.State == "complete" || op.State == "error" {
			return op
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return operationResponse{}
}

func TestProjectUpdateAcceptsOperationWithExplicitCompletion(t *testing.T) {
	updater := &projectUpdaterStub{}
	members := []groups.Container{
		{ID: "web1", Project: "shop", Service: "web", UpdateAvailable: true},
		{ID: "web2", Project: "shop", Service: "web", UpdateAvailable: true},
		{ID: "db1", Project: "shop", Service: "db"},
		{ID: "other", Project: "other", Service: "api", UpdateAvailable: true},
	}
	s := projectTestServer(members, updater)
	op := submitProject(t, s, "shop")
	op = awaitOperation(t, s, op.ID)
	if op.State != "complete" || op.StartedAt == "" || op.FinishedAt == "" || len(updater.members) != 3 || len(op.Messages) < 2 {
		t.Fatalf("operation=%+v members=%v", op, updater.members)
	}
	if _, err := time.Parse(time.RFC3339Nano, op.QueuedAt); err != nil {
		t.Fatal(err)
	}
}
