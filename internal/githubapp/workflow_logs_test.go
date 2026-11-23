package githubapp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Verbatim Logs workflow and publisher blobs from everyonesoftware/logs.
func logsFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLogsWorkflowRejectsDriftAndAmbiguity(t *testing.T) {
	workflow := logsFixture(t, "logs-ci.yml")
	script := logsFixture(t, "logs-publish-web-image.mjs")
	installed, err := provisionLogsWorkflow(workflow)
	if err != nil {
		t.Fatal(err)
	}
	adapted, err := provisionLogsPublisher(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, workflow, script, second, problem string }{
		{"unknown workflow", strings.Replace(workflow, "queue: max", "queue: single", 1), script, "", "differs from the reviewed layout"},
		{"unknown publisher", workflow, script + "// changed\n", "", "differs from the reviewed source"},
		{"multiple producers", workflow, script, conventionalWorkflow, "exactly one"},
		{"missing outputs", installed, script, "", "missing verified registry outputs"},
		{"wrong digest", strings.Replace(installed, logsCaptureMarker, "steps.publish.outputs.digest", 1), adapted, "", "incomplete or custom"},
		{"unguarded capture", strings.Replace(installed, "steps.logs_registry.outcome == 'success' && ", "", 1), adapted, "", "incomplete or custom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := testApp(t)
			sha := baseSHA
			files := map[string]string{".github/workflows/ci.yml": tc.workflow, logsPublisherPath: tc.script, senderPath: senderTemplate, callbackPath: callbackTemplate, connectionPath: connectionTemplate}
			if tc.second != "" {
				files[".github/workflows/second.yml"] = tc.second
			}
			c := fakeClient(t, planTransport(t, files, &sha, nil))
			p, err := c.PlanWorkflow(context.Background(), app, repository)
			if err != nil || p.Supported || len(p.Files) != 0 || p.Fingerprint != "" || !strings.Contains(strings.Join(p.Problems, " "), tc.problem) {
				t.Fatalf("drift result: %+v, %v", p, err)
			}
		})
	}
}

func TestLogsNotifyAllowsWebOnlyRelease(t *testing.T) {
	installed, err := provisionLogsWorkflow(logsFixture(t, "logs-ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := workflowYAML(installed)
	if err != nil {
		t.Fatal(err)
	}
	jobs := yamlField(root, "jobs")
	release := yamlField(jobs, "release")
	for _, native := range []string{"android", "windows"} {
		allowedSkip := "needs." + native + ".result == 'skipped' && needs.validate.outputs." + native + " == 'false'"
		if !needsJob(release, native) || !strings.Contains(condition(yamlValue(release, "if")), condition(allowedSkip)) {
			t.Fatalf("fixture no longer allows a Web-only release with %s skipped", native)
		}
	}
	notify := yamlField(jobs, "services_notify")
	want := "!cancelled() && " + publicationEventGate + " && needs.release.result == 'success' && needs.release.outputs.services_artifact != '' && vars.SERVICES_PUBLISH_NOTIFICATIONS == 'true'"
	if !needsJob(notify, "release") || condition(yamlValue(notify, "if")) != condition(want) {
		t.Fatalf("Web-only release needs an explicit cancellation check instead of implicit success: %s", yamlValue(notify, "if"))
	}
}

func TestLogsInspectValidatesProducerAtLegacyPath(t *testing.T) {
	workflow, err := provisionLogsWorkflow(logsFixture(t, "logs-ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	original := logsFixture(t, "logs-publish-web-image.mjs")
	adapted, err := provisionLogsPublisher(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, script string
		supported    bool
	}{
		{"installed", adapted, true},
		{"missing", "", false},
		{"unadapted", original, false},
		{"drifted", adapted + "// changed\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				".github/workflows/verify-and-publish.yml": workflow,
				senderPath: senderTemplate, callbackPath: callbackTemplate, connectionPath: connectionTemplate,
			}
			if tc.script != "" {
				files[logsPublisherPath] = tc.script
			}
			assertHookCompatibility(t, files, tc.supported)
		})
	}
}

func TestLogsGeneratedPublisherSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is not installed")
	}
	adapted, err := provisionLogsPublisher(logsFixture(t, "logs-publish-web-image.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "publish-web-image.mjs")
	if err := os.WriteFile(path, []byte(adapted), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("generated publisher syntax: %v: %s", err, output)
	}
}

func TestLogsWorkflowLifecycle(t *testing.T) {
	for _, newline := range []string{"LF", "CRLF"} {
		t.Run(newline, func(t *testing.T) {
			ctx := context.Background()
			app, _ := testApp(t)
			sha := baseSHA
			workflow := logsFixture(t, "logs-ci.yml")
			script := logsFixture(t, "logs-publish-web-image.mjs")
			path := ".github/workflows/ci.yml"
			scriptPath := "scripts/publish-web-image.mjs"
			if newline == "CRLF" {
				workflow = strings.ReplaceAll(workflow, "\n", "\r\n")
				script = strings.ReplaceAll(script, "\n", "\r\n")
			}
			files := map[string]string{path: workflow, scriptPath: script}
			c := fakeClient(t, planTransport(t, files, &sha, nil))
			for attempt := 0; attempt < 2; attempt++ {
				plan, err := c.PlanWorkflow(ctx, app, repository)
				want := 5
				if attempt != 0 {
					want = 2
				} // Retain shared sender/helper files on removal.
				if err != nil || !plan.Supported || len(plan.Files) != want {
					t.Fatalf("Logs setup supported=%v changes=%d problems=%v err=%v", plan.Supported, len(plan.Files), plan.Problems, err)
				}
				for _, file := range plan.Files {
					if files[file.Path] != file.Before {
						t.Fatalf("stale before for %s", file.Path)
					}
					files[file.Path] = file.After
				}
				if err := validateNotificationHook(files[path]); err != nil {
					t.Fatal(err)
				}
				again, err := c.PlanWorkflow(ctx, app, repository)
				if err != nil || !again.Supported || len(again.Files) != 0 {
					t.Fatalf("repeat plan: %+v, %v", again, err)
				}
				assertHookCompatibility(t, files, true)
				if newline == "CRLF" {
					for _, p := range []string{path, scriptPath} {
						if strings.Contains(strings.ReplaceAll(files[p], "\r\n", ""), "\n") {
							t.Fatalf("mixed line endings in %s", p)
						}
					}
				}
				removal, err := c.PlanWorkflowRemoval(ctx, app, repository)
				if err != nil || !removal.Supported || len(removal.Files) != 2 {
					t.Fatalf("removal: %+v, %v", removal, err)
				}
				for _, file := range removal.Files {
					if file.After == "" {
						delete(files, file.Path)
					} else {
						files[file.Path] = file.After
					}
				}
				if files[path] != workflow {
					t.Fatal("removal did not restore exact workflow bytes")
				}
			}
		})
	}
}
