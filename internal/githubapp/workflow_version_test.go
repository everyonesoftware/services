package githubapp

import (
	"strings"
	"testing"
)

func TestConventionalCaptureKeepsAnExistingLiteralBuildVersionAsAString(t *testing.T) {
	workflow := strings.ReplaceAll(conventionalWorkflow, "APP_VERSION=${{ github.run_number }}", "APP_VERSION=123")
	after, err := provisionConventionalWorkflow(workflow)
	if err != nil {
		t.Fatal(err)
	}
	root, err := workflowYAML(after)
	if err != nil {
		t.Fatal(err)
	}
	steps := yamlField(yamlField(yamlField(root, "jobs"), "publish"), "steps")
	for _, step := range steps.Content {
		if yamlValue(step, "id") == "services_capture" {
			version := yamlField(yamlField(step, "env"), "APP_VERSION")
			if version == nil || version.Tag != "!!str" || version.Value != "123" {
				t.Fatal("existing version was not preserved as a string")
			}
			return
		}
	}
	t.Fatal("capture missing")
}
