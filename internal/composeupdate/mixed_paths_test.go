package composeupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"services/internal/groups"
	"strings"
	"testing"
)

func TestUpdateProjectAcceptsMixedWindowsAndMountedLabels(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mounted Unix namespace is exercised in the Linux container suite")
	}
	runner := &recordingRunner{}
	u, web, dir := testUpdater(t, runner)
	web.UpdateAvailable = true
	web.ComposeConfigFilesLabel = strings.Join(web.ComposeConfigFiles, ",")
	sidecar := web
	sidecar.Service = "tailscale"
	sidecar.UpdateAvailable = false
	sidecar.ComposeWorkingDir = dir
	sidecar.ComposeConfigFiles = []string{filepath.Join(dir, "compose.yaml"), filepath.Join(dir, "override.yaml")}
	sidecar.ComposeConfigFilesLabel = strings.Join(sidecar.ComposeConfigFiles, ",")
	if err := u.UpdateProject(context.Background(), []groups.Container{sidecar, web}); err != nil {
		t.Fatalf("same project with mixed path labels rejected: %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("want one pull and up, got %v", runner.calls)
	}
	for _, call := range runner.calls {
		if call.workingDir != dir {
			t.Fatalf("wrong directory %q", call.workingDir)
		}
	}
}

func TestMountedLabelAliasesCannotEscapeRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires mounted Unix namespace")
	}
	for _, variant := range []string{"sibling", "traversal", "symlink", "different-case"} {
		t.Run(variant, func(t *testing.T) {
			runner := &recordingRunner{}
			u, web, _ := testUpdater(t, runner)
			sibling := u.mountRoot + "-outside"
			if err := os.MkdirAll(sibling, 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.RemoveAll(sibling) })
			outside := filepath.Join(sibling, "compose.yaml")
			if err := os.WriteFile(outside, []byte("services: {}"), 0600); err != nil {
				t.Fatal(err)
			}
			candidate := outside
			switch variant {
			case "traversal":
				candidate = u.mountRoot + "/../" + filepath.Base(sibling) + "/compose.yaml"
			case "symlink":
				link := filepath.Join(u.mountRoot, "outside")
				if err := os.Symlink(sibling, link); err != nil {
					t.Fatal(err)
				}
				candidate = filepath.Join(link, "compose.yaml")
			case "different-case":
				candidate = strings.ToUpper(u.mountRoot) + "/shop/compose.yaml"
			}
			web.ComposeConfigFilesLabel = candidate
			if err := u.Pull(context.Background(), web); err == nil {
				t.Fatalf("accepted escaped alias %q", candidate)
			}
			if len(runner.calls) > 0 {
				t.Fatal("executed before validation")
			}
		})
	}
}
