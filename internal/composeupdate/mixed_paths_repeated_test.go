package composeupdate

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"services/internal/groups"
)

func TestProjectCanUpdateAgainAfterComposeRewritesSelectedServiceLabels(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("production mounted Unix namespace requires Linux")
	}
	runner := &recordingRunner{}
	updater, app, dir := testUpdater(t, runner)
	app.UpdateAvailable = true
	app.ComposeConfigFilesLabel = strings.Join(app.ComposeConfigFiles, ",")
	sidecar := app
	sidecar.Service = "tailscale"
	sidecar.UpdateAvailable = false
	if err := updater.UpdateProject(context.Background(), []groups.Container{app, sidecar}); err != nil {
		t.Fatal(err)
	}
	// Compose recreated only app from inside Services. The sibling retains its
	// Windows labels, just as in the user's Agent/Tailscale deployment.
	app.ComposeWorkingDir = dir
	app.ComposeConfigFiles = []string{filepath.Join(dir, "compose.yaml"), filepath.Join(dir, "override.yaml")}
	app.ComposeConfigFilesLabel = strings.Join(app.ComposeConfigFiles, ",")
	if err := updater.UpdateProject(context.Background(), []groups.Container{app, sidecar}); err != nil {
		t.Fatalf("second project update rejected after label rewrite: %v", err)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("want two pull/up pairs; got %d commands", len(runner.calls))
	}
	for i := 0; i < 2; i++ {
		if !reflect.DeepEqual(runner.calls[i], runner.calls[i+2]) {
			t.Fatalf("path namespaces changed operation scope: first=%+v second=%+v", runner.calls[i], runner.calls[i+2])
		}
	}
}
