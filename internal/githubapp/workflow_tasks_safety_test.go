package githubapp

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTasksWorkflowPreservesSupportedSettings(t *testing.T) {
	original := tasksWorkflow(t)
	for name, change := range map[string][2]string{
		"no defaults":              {"defaults:\n  run:\n    shell: bash\n\n", ""},
		"no concurrency":           {"concurrency:\n  group: publish-main\n  cancel-in-progress: false\n\n", ""},
		"queue max":                {"  cancel-in-progress: false\n", "  cancel-in-progress: false\n  queue: max\n"},
		"queue single":             {"  cancel-in-progress: false\n", "  cancel-in-progress: false\n  queue: single\n"},
		"default non-cancellation": {"  cancel-in-progress: false\n", ""},
		"wrapped branch guard":     {"if: github.ref == 'refs/heads/main'", "if: ${{github.ref=='refs/heads/main'}}"},
		"other branch guard":       {"refs/heads/main", "refs/heads/release/stable"},
		"explicit tag shell":       {"        id: tags\n", "        id: tags\n        shell: bash\n"},
		"other job name":           {"  container:\n", "  publish_image:\n"},
	} {
		t.Run(name, func(t *testing.T) {
			workflow := replaceWorkflowOnce(t, original, change[0], change[1])
			after, err := provisionConventionalWorkflow(workflow)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateNotificationHook(after); err != nil {
				t.Fatal(err)
			}
			beforeRoot, _ := workflowYAML(workflow)
			afterRoot, _ := workflowYAML(after)
			for _, field := range []string{"defaults", "concurrency"} {
				beforeField := yamlField(beforeRoot, field)
				if beforeField != nil {
					beforeText, _ := yaml.Marshal(beforeField)
					afterText, _ := yaml.Marshal(yamlField(afterRoot, field))
					if string(beforeText) != string(afterText) {
						t.Fatalf("%s changed", field)
					}
				}
			}
		})
	}
}

func TestTasksWorkflowRejectsUnsafeExecutionAndBindings(t *testing.T) {
	original := tasksWorkflow(t)
	hook, err := provisionConventionalWorkflow(original)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string][2]string{
		"custom default shell":       {"    shell: bash\n", "    shell: bash {0}\n"},
		"expression default shell":   {"    shell: bash\n", "    shell: ${{ vars.SHELL }}\n"},
		"default working directory":  {"    shell: bash\n", "    shell: bash\n    working-directory: apps/android\n"},
		"malformed defaults":         {"defaults:\n  run:\n    shell: bash", "defaults: bash"},
		"unrelated defaults":         {"defaults:\n", "defaults:\n  unknown: value\n"},
		"conditional tag producer":   {"        id: tags\n", "        id: tags\n        if: false\n"},
		"ignored tag failure":        {"        id: tags\n", "        id: tags\n        continue-on-error: true\n"},
		"custom tag shell":           {"        id: tags\n", "        id: tags\n        shell: echo {0}\n"},
		"custom tag directory":       {"        id: tags\n", "        id: tags\n        working-directory: other\n"},
		"missing tag producer":       {"        id: tags\n", "        id: other_tags\n"},
		"ambiguous tag execution":    {"        id: tags\n", "        id: tags\n        uses: other/action@v1\n"},
		"false version source":       {"if: github.ref == 'refs/heads/main'", "if: false"},
		"arbitrary version guard":    {"if: github.ref == 'refs/heads/main'", "if: always()"},
		"ignored source job failure": {"  validate:\n", "  validate:\n    continue-on-error: true\n"},
		"conditional version step":   {"        id: version\n", "        id: version\n        if: false\n"},
		"missing version dependency": {"    needs: validate\n", ""},
		"consumer status override":   {"  container:\n", "  container:\n    if: always()\n"},
		"ignored consumer failure":   {"  container:\n", "  container:\n    continue-on-error: true\n"},
	} {
		t.Run(name, func(t *testing.T) {
			workflow := replaceWorkflowOnce(t, original, change[0], change[1])
			if after, err := provisionConventionalWorkflow(workflow); err == nil || after != "" {
				t.Fatalf("unsafe automatic adaptation accepted: %v", err)
			}
			changedHook := replaceWorkflowOnce(t, hook, change[0], change[1])
			files := map[string]string{
				".github/workflows/publish.yml": changedHook,
				senderPath:                      senderTemplate, callbackPath: callbackTemplate, connectionPath: connectionTemplate,
			}
			assertHookCompatibility(t, files, false)
		})
	}
}

func TestTasksWorkflowRejectsUnsafeExistingConcurrency(t *testing.T) {
	original := tasksWorkflow(t)
	for name, change := range map[string][2]string{
		"scalar":                   {"concurrency:\n  group: publish-main\n  cancel-in-progress: false", "concurrency: publish-main"},
		"cancelling":               {"cancel-in-progress: false", "cancel-in-progress: true"},
		"conditional cancellation": {"cancel-in-progress: false", "cancel-in-progress: ${{ github.ref != 'refs/heads/main' }}"},
		"ref partition":            {"group: publish-main", "group: publish-${{ github.ref }}"},
		"run partition":            {"group: publish-main", "group: publish-${{ github.run_id }}"},
		"missing group":            {"  group: publish-main\n", ""},
		"empty group":              {"group: publish-main", "group: ''"},
		"unsupported queue":        {"  cancel-in-progress: false", "  cancel-in-progress: false\n  queue: unknown"},
		"unknown setting":          {"  cancel-in-progress: false", "  cancel-in-progress: false\n  unknown: true"},
	} {
		t.Run(name, func(t *testing.T) {
			workflow := replaceWorkflowOnce(t, original, change[0], change[1])
			if after, err := provisionConventionalWorkflow(workflow); err == nil || after != "" || !strings.Contains(err.Error(), "concurrency") {
				t.Fatalf("unsafe concurrency accepted or unclear error: %v", err)
			}
		})
	}
}

func replaceWorkflowOnce(t *testing.T, workflow, old, replacement string) string {
	t.Helper()
	if !strings.Contains(workflow, old) {
		t.Fatalf("mutation did not match %q", old)
	}
	return strings.Replace(workflow, old, replacement, 1)
}
