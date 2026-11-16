package githubapp

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestPlanRemovalConfirmsAbsenceOnDefaultBranch(t *testing.T) {
	app, _ := testApp(t)
	sha := baseSHA
	files := map[string]string{".github/workflows/publish.yml": conventionalWorkflow, senderPath: senderTemplate}
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
	if err != nil || !plan.Supported || len(plan.Files) != 0 || len(plan.Fingerprint) != 64 {
		t.Fatalf("%+v %v", plan, err)
	}
}

func TestPlanRemovalPreservesPublishersAndSharedHelpers(t *testing.T) {
	app, _ := testApp(t)
	agent, err := os.ReadFile("testdata/agent-publish-image.yml")
	if err != nil {
		t.Fatal(err)
	}
	direct := strings.ReplaceAll(conventionalWorkflow, "        id: push\n", "")
	direct = strings.ReplaceAll(direct, "          tags: ${{ steps.meta.outputs.tags }}", "          tags: |\n            ghcr.io/${{ github.repository }}:${{ github.run_number }}\n            ghcr.io/${{ github.repository }}:latest")
	direct = strings.ReplaceAll(direct, "    runs-on:", "    outputs:\n      old_digest: ${{ steps.services_push.outputs.digest }}\n    runs-on:")
	for name, original := range map[string]string{"conventional": conventionalWorkflow, "agent": string(agent), "tasks": tasksWorkflow(t), "direct": direct} {
		t.Run(name, func(t *testing.T) {
			installed, err := provisionConventionalWorkflow(original)
			if err != nil {
				t.Fatal(err)
			}
			for _, ending := range []string{"\n", "\r\n"} {
				workflow := strings.ReplaceAll(installed, "\n", ending)
				files := map[string]string{".github/workflows/publish.yml": workflow, senderPath: senderTemplate, callbackPath: callbackTemplate, connectionPath: connectionTemplate}
				sha := baseSHA
				c := fakeClient(t, planTransport(t, files, &sha, nil))
				plan, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
				if err != nil || !plan.Supported || len(plan.Files) != 2 {
					t.Fatalf("%+v %v", plan, err)
				}
				for _, f := range plan.Files {
					if f.Before != files[f.Path] {
						t.Fatal("inexact before")
					}
					if f.Path == senderPath || f.Path == callbackPath {
						t.Fatal("shared helper deleted")
					}
					if f.After == "" {
						delete(files, f.Path)
					} else {
						files[f.Path] = f.After
					}
				}
				after := files[".github/workflows/publish.yml"]
				if ending == "\r\n" && strings.Contains(strings.ReplaceAll(after, "\r\n", ""), "\n") {
					t.Fatal("line endings changed")
				}
				if strings.Contains(after, "publish-notification.mjs") || strings.Contains(after, "services_capture") || strings.Contains(after, "services_notify:") || strings.Contains(after, "services_artifact:") {
					t.Fatal(after)
				}
				remaining := after
				for _, line := range strings.Split(original, "\n") {
					if strings.TrimSpace(line) == "" {
						continue
					}
					at := strings.Index(remaining, line)
					if at < 0 {
						t.Fatalf("lost original line %q", line)
					}
					remaining = remaining[at+len(line):]
				}
				again, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
				if err != nil || !again.Supported || len(again.Files) != 0 {
					t.Fatalf("not absent: %+v %v", again, err)
				}
			}
		})
	}
	files := fixtureFiles(t)
	sha := baseSHA
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
	if err != nil || !plan.Supported || len(plan.Files) != 2 {
		t.Fatalf("Services: %+v %v", plan, err)
	}
	for _, f := range plan.Files {
		if strings.HasSuffix(f.Path, "verify-and-publish.yml") {
			if strings.Contains(f.After, "notification-failure:") || strings.Contains(f.After, "steps.capture.") || strings.Contains(f.After, "needs: [publish, notify]") {
				t.Fatal(f.After)
			}
			for _, line := range []string{"      digest: ${{ steps.push.outputs.digest }}", "      version: ${{ needs.verify.outputs.version }}", "          build-args: APP_VERSION=${{ needs.verify.outputs.version }}", "      - name: Run publish notification tests"} {
				if !strings.Contains(f.After, line) {
					t.Fatalf("lost unrelated line %q", line)
				}
			}
		}
	}
}
