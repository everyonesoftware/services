package composeupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"services/internal/groups"
)

type recordedCall struct {
	workingDir string
	args       []string
}

type recordingRunner struct {
	calls  []recordedCall
	failAt int
}

func (r *recordingRunner) Run(_ context.Context, workingDir string, args []string) error {
	r.calls = append(r.calls, recordedCall{workingDir: workingDir, args: append([]string(nil), args...)})
	if r.failAt > 0 && len(r.calls) == r.failAt {
		return errors.New("runner failed")
	}
	return nil
}

func testUpdater(t *testing.T, runner *recordingRunner) (*Updater, groups.Container, string) {
	t.Helper()
	root := t.TempDir()
	projectDir := filepath.Join(root, "shop")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"compose.yaml", "override.yaml"} {
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte("services: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	container := groups.Container{
		Project:            "shop",
		Service:            "web",
		ComposeWorkingDir:  `E:\Containers\shop`,
		ComposeConfigFiles: []string{`E:\Containers\shop\compose.yaml`, `E:\Containers\shop\override.yaml`},
	}
	return NewWithRunner(`E:\Containers`, root, "/var/run/docker.sock", runner), container, projectDir
}

func TestPullTargetsOnlyTheSelectedComposeService(t *testing.T) {
	runner := &recordingRunner{}
	updater, container, projectDir := testUpdater(t, runner)

	if err := updater.Pull(context.Background(), container); err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("got %d Compose calls, want 1", len(runner.calls))
	}
	call := runner.calls[0]
	wantArgs := []string{
		"--project-name", "shop",
		"--project-directory", projectDir,
		"--file", filepath.Join(projectDir, "compose.yaml"),
		"--file", filepath.Join(projectDir, "override.yaml"),
		"pull", "web",
	}
	if call.workingDir != projectDir || !reflect.DeepEqual(call.args, wantArgs) {
		t.Fatalf("Compose call = (%q, %v), want (%q, %v)", call.workingDir, call.args, projectDir, wantArgs)
	}
}

func TestUpdatePullsThenWaitsForSelectedServiceToBeRunning(t *testing.T) {
	runner := &recordingRunner{}
	updater, container, _ := testUpdater(t, runner)

	if err := updater.Update(context.Background(), container); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("got %d Compose calls, want pull then up", len(runner.calls))
	}
	if got := runner.calls[0].args[len(runner.calls[0].args)-2:]; !reflect.DeepEqual(got, []string{"pull", "web"}) {
		t.Fatalf("first call tail = %v, want [pull web]", got)
	}
	wantTail := []string{"up", "-d", "--no-deps", "--pull", "never", "--no-build", "--wait", "--wait-timeout", "300", "web"}
	gotTail := runner.calls[1].args[len(runner.calls[1].args)-len(wantTail):]
	if !reflect.DeepEqual(gotTail, wantTail) {
		t.Fatalf("update call tail = %v, want %v", gotTail, wantTail)
	}
}

func TestUpdateStopsIfPullFails(t *testing.T) {
	runner := &recordingRunner{failAt: 1}
	updater, container, _ := testUpdater(t, runner)

	if err := updater.Update(context.Background(), container); err == nil {
		t.Fatal("Update() error = nil, want pull failure")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("got %d Compose calls, want only the failed pull", len(runner.calls))
	}
}

func TestPullRejectsComposeFilesOutsideConfiguredReadOnlyRoot(t *testing.T) {
	runner := &recordingRunner{}
	updater, container, _ := testUpdater(t, runner)
	container.ComposeConfigFiles = []string{`C:\Users\public\compose.yaml`}

	if err := updater.Pull(context.Background(), container); err == nil {
		t.Fatal("Pull() error = nil, want path outside root to be rejected")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner got %d calls, want no command for an untrusted path", len(runner.calls))
	}
}

func TestPullRejectsDriveRelativeConfiguredPaths(t *testing.T) {
	cases := []struct {
		name        string
		hostRoot    string
		workingDir  string
		configFiles []string
	}{
		{
			name:        "drive-relative root and labels",
			hostRoot:    `E:Containers`,
			workingDir:  `E:Containers\shop`,
			configFiles: []string{`E:Containers\shop\compose.yaml`},
		},
		{
			name:        "drive-relative config file",
			hostRoot:    `E:\Containers`,
			workingDir:  `E:\Containers\shop`,
			configFiles: []string{`E:Containers\shop\compose.yaml`},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &recordingRunner{}
			updater, container, _ := testUpdater(t, runner)
			updater.hostRoot = testCase.hostRoot
			container.ComposeWorkingDir = testCase.workingDir
			container.ComposeConfigFiles = testCase.configFiles

			if err := updater.Pull(context.Background(), container); err == nil {
				t.Fatal("Pull() error = nil, want drive-relative paths rejected")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner got %d calls, want no command for invalid paths", len(runner.calls))
			}
		})
	}
}

func TestRelativeHostPathUsesASCIIOnlyCaseFoldingForWindowsPrefixes(t *testing.T) {
	if got, err := relativeHostPath(`E:/Containers`, `e:/containers/shop`, true); err != nil || got != "shop" {
		t.Fatalf("relativeHostPath() = (%q, %v), want (shop, nil)", got, err)
	}
	if _, err := relativeHostPath(`E:/Containers/K`, `e:/containers/K/shop`, true); err == nil {
		t.Fatal("relativeHostPath() accepted a non-ASCII case-equivalent root component")
	}
	if got, err := relativeHostPath(`E:/Containers/Å`, `e:/containers/Å/shop`, true); err != nil || got != "shop" {
		t.Fatalf("relativeHostPath() with an exact Unicode prefix = (%q, %v), want (shop, nil)", got, err)
	}
}

func TestWithinUsesContainerNamespacePathSemantics(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "Containers")
	if !within(root, filepath.Join(root, "shop")) {
		t.Fatal("within() rejected a child in the mounted container root")
	}
	caseVariantSibling := filepath.Join(parent, "containers", "shop")
	if within(root, caseVariantSibling) {
		t.Fatal("within() accepted a case-variant sibling outside the container root")
	}
}

func TestPullResolvesAbsoluteUnixComposePaths(t *testing.T) {
	runner := &recordingRunner{}
	root := t.TempDir()
	projectDir := filepath.Join(root, "shop")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(projectDir, "compose.yaml")
	if err := os.WriteFile(configFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	container := groups.Container{
		Project:            "shop",
		Service:            "web",
		ComposeWorkingDir:  projectDir,
		ComposeConfigFiles: []string{configFile},
	}
	updater := NewWithRunner(root, root, "/var/run/docker.sock", runner)

	if err := updater.Pull(context.Background(), container); err != nil {
		t.Fatalf("Pull() error = %v, want absolute Unix paths accepted", err)
	}
	if len(runner.calls) != 1 || runner.calls[0].workingDir != projectDir {
		t.Fatalf("calls = %+v, want one call from %q", runner.calls, projectDir)
	}
}

func TestComposeArgsResolvesACommaInsideAConfigFilename(t *testing.T) {
	runner := &recordingRunner{}
	updater, container, projectDir := testUpdater(t, runner)
	commaFile := filepath.Join(projectDir, "compose,review.yaml")
	if err := os.WriteFile(commaFile, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	container.ComposeConfigFilesLabel = `E:\Containers\shop\compose,review.yaml`

	_, args, err := updater.composeArgs(container)
	if err != nil {
		t.Fatalf("composeArgs() error = %v", err)
	}
	var configFiles []string
	for index, arg := range args {
		if arg == "--file" && index+1 < len(args) {
			configFiles = append(configFiles, args[index+1])
		}
	}
	if !reflect.DeepEqual(configFiles, []string{commaFile}) {
		t.Fatalf("config files = %v, want the single comma-containing file %q", configFiles, commaFile)
	}
}

func TestComposeArgsRejectsAmbiguousConfigFileLabels(t *testing.T) {
	runner := &recordingRunner{}
	updater, container, projectDir := testUpdater(t, runner)
	// Use Unix host labels so both possible parses have filesystem-valid
	// mounted filenames even when this security test runs on Windows.
	updater.hostRoot = "/Containers"
	container.ComposeWorkingDir = "/Containers/shop"
	ambiguousDirectory := filepath.Join(projectDir, "compose.yaml,", "Containers", "shop")
	if err := os.MkdirAll(ambiguousDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ambiguousDirectory, "override.yaml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	container.ComposeConfigFilesLabel = `/Containers/shop/compose.yaml,/Containers/shop/override.yaml`

	if _, _, err := updater.composeArgs(container); err == nil {
		t.Fatal("composeArgs() error = nil, want ambiguous config label rejected")
	}
}

func TestComposeEnvironmentUsesFixedRuntimePathAndHome(t *testing.T) {
	environment := []string{
		"PATH=/tmp/untrusted-bin",
		"HOME=/root/.docker",
		"DOCKER_HOST=tcp://attacker.example",
		"DOCKER_CONTEXT=untrusted",
		"COMPOSE_FILE=/tmp/override.yaml",
		"COMPOSE_PROJECT_NAME=override",
		"SERVICES_COMPOSE_HOST_ROOT=E:/Containers",
		"APP_CONFIG=must-not-pass",
	}

	got := composeEnvironment(environment)
	want := []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/tmp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("composeEnvironment() = %v, want fixed PATH and HOME", got)
	}
}

func TestExecRunnerRejectsSocketPathsOutsideTheContainerNamespace(t *testing.T) {
	runner := execRunner{dockerSocket: `E:\Docker\docker.sock`}
	err := runner.Run(context.Background(), t.TempDir(), nil)
	if err == nil || err.Error() != "Docker socket path must be absolute inside the Services container" {
		t.Fatalf("Run() error = %v, want container-absolute socket path error", err)
	}
}
