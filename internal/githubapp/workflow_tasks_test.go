package githubapp

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Verbatim everyonesoftware/tasks publish.yml, blob
// 99174c9c3085432eaa08e04bb86d06aa5b980626. No live network is used by tests.
func tasksWorkflow(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/tasks-publish.yml")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTasksWorkflowPlanValidateRepeatInspect(t *testing.T) {
	original := tasksWorkflow(t)
	for name, workflow := range map[string]string{
		"verbatim":           original,
		"CRLF":               strings.ReplaceAll(original, "\n", "\r\n"),
		"expression spacing": strings.ReplaceAll(original, "${{ needs.validate.outputs.version }}", "${{needs.validate.outputs.version}}"),
	} {
		t.Run(name, func(t *testing.T) {
			app, _ := testApp(t)
			sha := baseSHA
			path := ".github/workflows/publish.yml"
			files := map[string]string{path: workflow}
			c := fakeClient(t, planTransport(t, files, &sha, nil))
			plan, err := c.PlanWorkflow(context.Background(), app, repository)
			if err != nil || !plan.Supported || len(plan.Files) != 4 {
				t.Fatalf("Tasks setup: %+v error=%v", plan, err)
			}
			for _, file := range plan.Files {
				files[file.Path] = file.After
			}
			after := files[path]
			if err := validateNotificationHook(after); err != nil {
				t.Fatal(err)
			}
			// Source-preserving adaptation must retain every original line in order,
			// including pinned actions, comments, scripts, and unrelated Android work.
			rest := after
			for _, line := range strings.Split(workflow, "\n") {
				line += "\n"
				if line == "\n" { // The original final newline is already consumed.
					continue
				}
				i := strings.Index(rest, line)
				if i < 0 {
					t.Fatalf("original workflow line lost or reordered: %q", line)
				}
				rest = rest[i+len(line):]
			}
			normalized := strings.ReplaceAll(after, "\r\n", "\n")
			if strings.Count(normalized, "\nconcurrency:") != 1 || strings.Contains(normalized, "services-publish-${{ github.repository }}") {
				t.Fatal("existing concurrency was replaced or duplicated")
			}
			if !containsAll(scalarBinding(after), "PUBLISHED_TAGS: ${{steps.tags.outputs.tags}}", "APP_VERSION: ${{needs.validate.outputs.version}}", "PUBLISHED_DIGEST: ${{steps.services_push.outputs.digest}}", "needs.container.result == 'success'") {
				t.Fatal("capture does not bind to Tasks' original publication")
			}
			if name == "CRLF" && strings.Contains(strings.ReplaceAll(after, "\r\n", ""), "\n") {
				t.Fatal("mixed line endings")
			}
			again, err := c.PlanWorkflow(context.Background(), app, repository)
			if err != nil || !again.Supported || len(again.Files) != 0 {
				t.Fatalf("repeat plan: %+v error=%v", again, err)
			}
			assertHookCompatibility(t, files, true)
		})
	}
}
