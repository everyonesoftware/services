package githubapp

import (
	"context"
	"strings"
	"testing"
)

func TestPlanConventionalDirectTagsAddsMissingPushIDAndKeepsExistingOutputs(t *testing.T) {
	app, _ := testApp(t)
	sha := baseSHA
	workflow := strings.ReplaceAll(conventionalWorkflow, "        id: push\n", "")
	workflow = strings.ReplaceAll(workflow, "          tags: ${{ steps.meta.outputs.tags }}", "          tags: |\n            ghcr.io/${{ github.repository }}:${{ github.run_number }}\n            ghcr.io/${{ github.repository }}:latest")
	workflow = strings.ReplaceAll(workflow, "    runs-on:", "    outputs:\n      old_digest: ${{ steps.services_push.outputs.digest }}\n    runs-on:")
	files := map[string]string{".github/workflows/publish.yml": workflow}
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || !plan.Supported {
		t.Fatalf("%v %v", plan.Problems, err)
	}
	for _, f := range plan.Files {
		files[f.Path] = f.After
	}
	w := files[".github/workflows/publish.yml"]
	root, err := workflowYAML(w)
	if err != nil {
		t.Fatal(err)
	}
	outputs := yamlField(yamlField(yamlField(root, "jobs"), "publish"), "outputs")
	steps := yamlField(yamlField(yamlField(root, "jobs"), "publish"), "steps")
	capturedTags := ""
	for _, step := range steps.Content {
		if yamlValue(step, "id") == "services_capture" {
			capturedTags = yamlValue(yamlField(step, "env"), "PUBLISHED_TAGS")
		}
	}
	if capturedTags != "ghcr.io/${{ github.repository }}:${{ github.run_number }}\nghcr.io/${{ github.repository }}:latest" {
		t.Fatalf("captured tags differ from actual pushed references: %q", capturedTags)
	}
	if yamlValue(outputs, "old_digest") != "${{ steps.services_push.outputs.digest }}" || yamlValue(outputs, "services_artifact") == "" {
		t.Fatal("existing outputs lost")
	}
	if !containsAll(w, "        id: services_push", "PUBLISHED_DIGEST: ${{ steps.services_push.outputs.digest }}", "ghcr.io/${{ github.repository }}:latest", "APP_VERSION: ${{ github.run_number }}") {
		t.Fatal("lost actual publication metadata")
	}
	again, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || !again.Supported || len(again.Files) != 0 {
		t.Fatal("direct-tag generated hook not recognized", err)
	}
}
