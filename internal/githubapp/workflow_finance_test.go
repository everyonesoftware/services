package githubapp

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Verbatim everyonesoftware/finance publish.yml after publisher removal, blob
// 44676c61c1ddce1c6a76c9b76bb0b1d0e64cd7bc. Tests use no live network.
func TestFinanceWorkflowReconnect(t *testing.T) {
	data, err := os.ReadFile("testdata/finance-publish.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(ending, func(t *testing.T) {
			original := strings.ReplaceAll(string(data), "\n", ending)
			app, _ := testApp(t)
			sha := baseSHA
			const path = ".github/workflows/publish.yml"
			files := map[string]string{path: original}
			c := fakeClient(t, planTransport(t, files, &sha, nil))
			// Reconnect once from Finance's actual removed state, then again
			// after another Services-managed install/removal cycle.
			for cycle := 0; cycle < 2; cycle++ {
				before := files[path]
				plan, err := c.PlanWorkflow(context.Background(), app, repository)
				if err != nil || !plan.Supported || len(plan.Files) == 0 {
					t.Fatalf("reconnect %d: %+v error=%v", cycle, plan, err)
				}
				for _, file := range plan.Files {
					files[file.Path] = file.After
				}
				after := files[path]
				if err := validateNotificationHook(after); err != nil {
					t.Fatal(err)
				}
				rest := after
				for _, line := range strings.Split(before, ending) {
					if strings.TrimSpace(line) == "" {
						continue
					}
					at := strings.Index(rest, line+ending)
					if at < 0 {
						t.Fatalf("original source lost or reordered: %q", line)
					}
					rest = rest[at+len(line)+len(ending):]
				}
				if strings.Count(after, ending+"concurrency:") != 1 {
					t.Fatal("concurrency duplicated")
				}
				if ending == "\r\n" && strings.Contains(strings.ReplaceAll(after, ending, ""), "\n") {
					t.Fatal("mixed line endings")
				}
				again, err := c.PlanWorkflow(context.Background(), app, repository)
				if err != nil || !again.Supported || len(again.Files) != 0 {
					t.Fatalf("repeat plan: %+v error=%v", again, err)
				}
				assertHookCompatibility(t, files, true)
				removal, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
				if err != nil || !removal.Supported || len(removal.Files) == 0 {
					t.Fatalf("removal: %+v error=%v", removal, err)
				}
				for _, file := range removal.Files {
					if file.After == "" {
						delete(files, file.Path)
					} else {
						files[file.Path] = file.After
					}
				}
			}
		})
	}
}
