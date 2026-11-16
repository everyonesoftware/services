package githubapp

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestPlanRemovalFailsClosedOnCustomOrSharedHooks(t *testing.T) {
	installed, err := provisionConventionalWorkflow(conventionalWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"extra-notify-command":      installed + "      - run: deploy-production\n",
		"custom-notify-environment": strings.Replace(installed, "    name: Notify Services", "    environment: production\n    name: Notify Services", 1),
		"shared-dependent":          installed + "  shared:\n    needs: services_notify\n    runs-on: ubuntu-latest\n    steps:\n      - run: deploy-production\n",
		"shared-capture":            strings.Replace(installed, "      - name: Capture original", "      - run: echo '${{ steps.services_capture.outputs.artifact }}'\n      - name: Capture original", 1),
		"bracket-shared-capture":    installed + "  shared:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo '${{ needs[\"publish\"][\"outputs\"][\"services_artifact\"] }}'\n",
		"implicit-condition":        installed + "  shared:\n    if: needs['services_notify'].result == 'success'\n    runs-on: ubuntu-latest\n    steps:\n      - run: deploy-production\n",
		"wildcard-capture":          strings.Replace(installed, "  services_notify:", "      - run: echo '${{ toJSON(steps.*.outputs) }}'\n\n  services_notify:", 1),
		"custom-upload-command":     strings.Replace(installed, "        uses: actions/upload-artifact@v7", "        run: deploy-production\n        uses: actions/upload-artifact@v7", 1),
		"incomplete-hook":           strings.Replace(installed, "node scripts/publish-notification.mjs send", "node scripts/custom-services-sender.mjs", 1),
		"duplicate-notify-setup":    installed + "      - uses: actions/setup-node@v7\n        with:\n          node-version: '24'\n",
		"flow-outputs":              strings.Replace(installed, "    outputs:\n      services_artifact: ${{ steps.services_capture.outputs.artifact }}", "    outputs: {services_artifact: '${{ steps.services_capture.outputs.artifact }}'}", 1),
		"invalid-workflow":          "jobs: [",
		"invalid-jobs":              "on: push\njobs: unknown\n",
		"unknown-reusable":          "on: push\njobs:\n  notify:\n    uses: owner/custom/.github/workflows/notify.yml@main\n",
	}
	app, _ := testApp(t)
	for name, workflow := range cases {
		t.Run(name, func(t *testing.T) {
			sha := baseSHA
			files := map[string]string{".github/workflows/publish.yml": workflow, connectionPath: connectionTemplate}
			c := fakeClient(t, planTransport(t, files, &sha, nil))
			plan, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
			if err != nil || plan.Supported || len(plan.Problems) == 0 || len(plan.Files) != 0 {
				t.Fatalf("unsafe plan %+v %v", plan, err)
			}
		})
	}
}

func TestPlanRemovalRejectsCustomCallbackAndBindsApproval(t *testing.T) {
	app, _ := testApp(t)
	sha := baseSHA
	files := map[string]string{connectionPath: connectionTemplate + "# custom\n"}
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
	if err != nil || plan.Supported || len(plan.Files) != 0 {
		t.Fatalf("%+v %v", plan, err)
	}
	files[connectionPath] = connectionTemplate
	first, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
	if err != nil || !first.Supported || len(first.Files) != 1 || first.Files[0].After != "" {
		t.Fatalf("%+v %v", first, err)
	}
	sha = strings.Repeat("c", 40)
	second, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
	if err != nil || first.Fingerprint == second.Fingerprint {
		t.Fatal("base not bound", err)
	}
	// Installation approval is not interchangeable with cleanup approval.
	files = fixtureFiles(t)
	delete(files, connectionPath)
	c = fakeClient(t, planTransport(t, files, &sha, nil))
	install, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || !install.Supported || install.Fingerprint == "" {
		t.Fatal(err)
	}
	if _, err := c.RemoveWorkflow(context.Background(), app, repository, install.Fingerprint); err == nil {
		t.Fatal("install fingerprint accepted")
	}
}

func TestPlanRemovalRejectsIncompleteWorkflowListing(t *testing.T) {
	app, _ := testApp(t)
	for _, mode := range []string{"pagination", "null", "missing-file"} {
		t.Run(mode, func(t *testing.T) {
			sha := baseSHA
			normal := planTransport(t, map[string]string{}, &sha, nil)
			c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
				if strings.Contains(r.URL.Path, "/contents/.github/workflows") {
					if mode == "null" {
						return response(200, nil), nil
					}
					if mode == "missing-file" {
						if strings.HasSuffix(r.URL.Path, "/workflows") {
							return response(200, []map[string]string{{"path": ".github/workflows/publish.yml", "type": "file"}}), nil
						}
						return response(404, nil), nil
					}
					v := response(200, []any{})
					v.Header.Set("Link", `<https://api.github.com/next>; rel="next"`)
					return v, nil
				}
				return normal(r)
			})
			plan, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
			if err == nil && plan.Supported {
				t.Fatalf("incomplete listing confirmed absent: %+v", plan)
			}
		})
	}
}
