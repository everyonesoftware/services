package githubapp

import (
	"context"
	"strings"
	"testing"
)

const conventionalWorkflow = `name: Publish
on:
  push:
    branches: [main]
jobs:
  publish:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - uses: docker/metadata-action@v5
        id: meta
        with:
          images: ghcr.io/${{ github.repository }}
          tags: |
            type=raw,value=${{ github.run_number }}
            type=raw,value=latest
      - uses: docker/build-push-action@v6
        id: push
        with:
          push: true
          build-args: APP_VERSION=${{ github.run_number }}
          tags: ${{ steps.meta.outputs.tags }}
`

func TestPlanConventionalWorkflowCapturesActualPushAndPreservesVersion(t *testing.T) {
	app, _ := testApp(t)
	sha := baseSHA
	files := map[string]string{".github/workflows/publish.yml": conventionalWorkflow}
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || !plan.Supported || len(plan.Files) != 4 {
		t.Fatalf("%+v %v", plan, err)
	}
	for _, f := range plan.Files {
		files[f.Path] = f.After
	}
	w := files[".github/workflows/publish.yml"]
	if !containsAll(w, "PUBLISHED_DIGEST: ${{ steps.push.outputs.digest }}", "PUBLISHED_TAGS: ${{ steps.meta.outputs.tags }}", "APP_VERSION: ${{ github.run_number }}", "services_notify:", "needs.publish.outputs.services_artifact", "vars.SERVICES_PUBLISH_NOTIFICATIONS == 'true'", "actions/download-artifact@", "actions/upload-artifact@") {
		t.Fatal(w)
	}
	if strings.Count(w, "node scripts/publish-notification.mjs capture") != 1 || strings.Count(w, "node scripts/publish-notification.mjs send") != 1 {
		t.Fatal("duplicate hook")
	}
	again, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || !again.Supported || len(again.Files) != 0 {
		t.Fatalf("generated workflow not recognized: %+v %v", again, err)
	}
}

func TestPlanExplainsAmbiguousPublicationInsteadOfGuessing(t *testing.T) {
	app, _ := testApp(t)
	for _, workflow := range []string{
		strings.ReplaceAll(conventionalWorkflow, "APP_VERSION=${{ github.run_number }}", "OTHER_VERSION=unknown"),
		strings.ReplaceAll(conventionalWorkflow, "push: true", "push: ${{ github.event_name != 'pull_request' }}"),
		strings.ReplaceAll(conventionalWorkflow, "    runs-on:", "    strategy: {matrix: {arch: [amd64, arm64]}}\n    runs-on:"),
		strings.ReplaceAll(conventionalWorkflow, "ghcr.io/${{ github.repository }}", "docker.io/example/image"),
		conventionalWorkflow + "  another:\n    steps:\n      - uses: docker/build-push-action@v6\n        with:\n          push: true\n",
		conventionalWorkflow + "env:\n  OTHER: value\n",
		strings.ReplaceAll(conventionalWorkflow, "jobs:", "jobs: &shared"),
		strings.ReplaceAll(conventionalWorkflow, "          push: true", "          push: false\n          push: true"),
		strings.ReplaceAll(conventionalWorkflow, "        id: push", "        id: push\n        env:\n          APP_VERSION: 900"),
		strings.ReplaceAll(conventionalWorkflow, "      - uses: actions/checkout@v4", "      - uses: actions/checkout@v4\n        with:\n          ref: some-other-branch"),
		strings.ReplaceAll(conventionalWorkflow, "          push: true", "          push: true\n          context: https://github.com/other/project.git"),
		strings.ReplaceAll(conventionalWorkflow, "        id: meta", "        id: meta\n        if: false"),
		strings.ReplaceAll(conventionalWorkflow, "APP_VERSION=${{ github.run_number }}", "APP_VERSION=${{ steps.missing.outputs.version }}"),
	} {
		sha := baseSHA
		c := fakeClient(t, planTransport(t, map[string]string{".github/workflows/publish.yml": workflow}, &sha, nil))
		plan, err := c.PlanWorkflow(context.Background(), app, repository)
		if err != nil || plan.Supported || len(plan.Problems) == 0 || len(plan.Files) != 0 {
			t.Fatalf("unsafe plan: supported=%v problems=%v files=%d %v", plan.Supported, plan.Problems, len(plan.Files), err)
		}
	}
}
