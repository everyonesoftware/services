package operations

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"services/internal/groups"
)

type fixtureSource struct {
	calls   int
	members []groups.Container
}

func (f *fixtureSource) Containers() ([]groups.Container, error) { f.calls++; return f.members, nil }

type fixtureChecker struct {
	called bool
	count  int
}

func (f *fixtureChecker) CheckNow(_ context.Context, c []groups.Container) error {
	f.called = true
	f.count = len(c)
	for i := range c {
		c[i].UpdateAvailable = true
	}
	return nil
}
func (f *fixtureChecker) Apply([]groups.Container) {}

type fixtureUpdater struct{ calls int }

func (f *fixtureUpdater) Pull(context.Context, groups.Container) error { f.calls++; return nil }
func (f *fixtureUpdater) UpdateProjectVerified(ctx context.Context, c []groups.Container, image func(string) (string, error), verify func(groups.Container, string) error) error {
	f.calls++
	return errors.New("validation failure")
}
func TestWorkerFreshPreflightDurableFailureAndNoReplay(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "ops.db"))
	defer s.Close()
	o, _ := s.Submit("alpha", "update project", "", "")
	s.Claim()
	source := &fixtureSource{members: []groups.Container{{ID: "x", Project: "alpha", Service: "web", State: "running"}, {ID: "unrelated", Project: "private", Image: "private/image", State: "running"}}}
	check := &fixtureChecker{}
	up := &fixtureUpdater{}
	w := Executor{Store: s, Source: source, Checker: check, Updater: up, Engine: NewEngine("", nil), Project: "services"}
	if err := w.Execute(context.Background(), o.ID); err == nil {
		t.Fatal("expected durable failure")
	}
	got, _ := s.Get(o.ID)
	if got.State != "error" || got.Error != "validation failure" || source.calls != 1 || !check.called {
		t.Fatalf("preflight or result %+v", got)
	}
	if err := w.Execute(context.Background(), o.ID); err == nil {
		t.Fatal("duplicate execution must fail")
	}
	if check.count != 1 {
		t.Fatalf("registry preflight must be scoped to selected project, checked %d", check.count)
	}
	if up.calls != 1 {
		t.Fatal("duplicate command")
	}
}
