package operations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"services/internal/groups"
)

type imageGateUpdater struct{ validated bool }

func (*imageGateUpdater) Pull(context.Context, groups.Container) error {
	return errors.New("unexpected pull")
}
func (u *imageGateUpdater) UpdateProjectVerified(_ context.Context, c []groups.Container, image func(string) (string, error), _ func(groups.Container, string) error) error {
	if _, err := image(c[0].Image); err != nil {
		return err
	}
	u.validated = true
	return errors.New("fixture stops before applying")
}

func TestWorkerPersistsExpectedImageAndRejectsIncompatibleSelfImage(t *testing.T) {
	for _, self := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary image", true: "incompatible self image"}[self], func(t *testing.T) {
			s, _ := Open(filepath.Join(t.TempDir(), "operations.db"))
			defer s.Close()
			project := "alpha"
			if self {
				project = "services"
			}
			o, _ := s.Submit(project, "update project", "", "")
			s.Claim()
			docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"Id":"sha256:expected","Config":{"Labels":{}}}`))
			}))
			defer docker.Close()
			source := &fixtureSource{members: []groups.Container{{ID: "old", Name: "old-name", Project: project, Service: "services", State: "running", Image: "fixture/image:latest"}}}
			updater := &imageGateUpdater{}
			executor := Executor{Store: s, Source: source, Checker: &fixtureChecker{}, Updater: updater, Engine: NewEngine(docker.URL, docker.Client()), Project: "services", Service: "services"}
			if err := executor.Execute(context.Background(), o.ID); err == nil {
				t.Fatal("fixture must stop before apply")
			}
			got, _ := s.Get(o.ID)
			if self {
				if updater.validated {
					t.Fatal("incompatible self image passed apply gate")
				}
			} else if got.ExpectedImages["services"] != "sha256:expected" {
				t.Fatalf("expected image was not persisted: %+v", got)
			}
		})
	}
}
