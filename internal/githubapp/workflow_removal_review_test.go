package githubapp

import (
	"context"
	"strings"
	"testing"
)

func TestRemovalPreservesProducerNodeSetupForUnrelatedCommand(t *testing.T) {
	installed, err := provisionConventionalWorkflow(conventionalWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	const deploy = "      - run: node --run deploy\n"
	installed = strings.Replace(installed, "  services_notify:", deploy+"\n  services_notify:", 1)
	const setup = "      - name: Set up Node.js for Services payload capture\n        uses: actions/setup-node@v7\n        with:\n          node-version: '24'\n"
	if !strings.Contains(installed, setup) {
		t.Fatal("fixture missing producer setup")
	}
	for _, newline := range []string{"\n", "\r\n"} {
		before := strings.ReplaceAll(installed, "\n", newline)
		after, err := removeWorkflowHook(before)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(after, strings.ReplaceAll(setup, "\n", newline)) || !strings.Contains(after, strings.ReplaceAll(deploy, "\n", newline)) {
			t.Fatalf("producer Node setup or unrelated command lost:\n%s", after)
		}
		remaining := before
		for _, line := range strings.SplitAfter(after, newline) {
			at := strings.Index(remaining, line)
			if at < 0 {
				t.Fatalf("rewrote surviving bytes %q", line)
			}
			remaining = remaining[at+len(line):]
		}
	}
}

func TestRemovalRejectsAggregateProducerOutputConsumers(t *testing.T) {
	installed, err := provisionConventionalWorkflow(conventionalWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	for name, expression := range map[string]string{
		"output-object":   "toJSON(needs.publish.outputs)",
		"output-wildcard": "toJSON(needs.publish.outputs.*)",
		"output-brackets": "toJSON(needs['publish']['outputs'])",
		"dynamic-output":  "needs.publish.outputs[github.event.inputs.output]",
		"dynamic-job":     "toJSON(needs[github.event.inputs.job].outputs)",
		"all-needs":       "toJSON(needs)",
	} {
		t.Run(name, func(t *testing.T) {
			workflow := installed + "\n  summary:\n    needs: publish\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo '${{ " + expression + " }}'\n"
			if after, err := removeWorkflowHook(workflow); err == nil {
				t.Fatalf("altered output object still consumed; unsafe result:\n%s", after)
			}
		})
	}
}

func TestRemovalRejectsEscapedSurvivingExpressions(t *testing.T) {
	installed, err := provisionConventionalWorkflow(conventionalWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	for name, scalar := range map[string]string{
		"aggregate":   `"echo '${{ toJSON(ne\u0065ds.publish.outputs) }}'"`,
		"removed-job": `"echo '${{ ne\u0065ds.services_notify.result }}'"`,
	} {
		t.Run(name, func(t *testing.T) {
			workflow := installed + "\n  summary:\n    runs-on: ubuntu-latest\n    steps:\n      - run: " + scalar + "\n"
			if after, err := removeWorkflowHook(workflow); err == nil {
				t.Fatalf("escaped dependency survived:\n%s", after)
			}
		})
	}
}

func TestRemovalRejectsDanglingFailureJobConsumers(t *testing.T) {
	original := fixtureFiles(t)[".github/workflows/verify-and-publish.yml"]
	for name, consumer := range map[string]string{
		"scalar-need":   "    needs: notification-failure\n",
		"sequence-need": "    needs: [publish, notification-failure]\n",
		"expression":    "    if: needs['notification-failure'].result == 'success'\n",
		"wildcard":      "    if: contains(needs.*.result, 'failure')\n",
	} {
		t.Run(name, func(t *testing.T) {
			workflow := original + "\n  unrelated-summary:\n" + consumer + "    runs-on: ubuntu-latest\n    steps:\n      - run: echo summary\n"
			if after, err := removeWorkflowHook(workflow); err == nil {
				t.Fatalf("removed job still consumed; unsafe result:\n%s", after)
			}
		})
	}
}

func TestRemovalRejectsEscapedCustomCallback(t *testing.T) {
	workflow := `on: push
jobs:
  callback:
    runs-on: ubuntu-latest
    steps:
      - run: "node scripts/publisher-connecti\u006fn-test.mjs"
        env:
          "SERVIC\u0045S_PUBLISH_URL": https://example.invalid
          "SERVIC\u0045S_PUBLISH_TOKEN": token
`
	if removalSignal(workflow) {
		t.Fatal("fixture must hide raw signals")
	}
	app, _ := testApp(t)
	sha := baseSHA
	for name, original := range map[string]string{"custom-only": workflow, "alongside-installed-hook": ""} {
		t.Run(name, func(t *testing.T) {
			if original == "" {
				installed, err := provisionConventionalWorkflow(conventionalWorkflow)
				if err != nil {
					t.Fatal(err)
				}
				original = installed + strings.TrimPrefix(workflow, "on: push\njobs:\n")
			}
			c := fakeClient(t, planTransport(t, map[string]string{".github/workflows/custom.yml": original}, &sha, nil))
			plan, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
			if err != nil || plan.Supported || len(plan.Problems) == 0 || len(plan.Files) != 0 {
				t.Fatalf("escaped custom callback certified absent: %+v %v", plan, err)
			}
		})
	}
}
