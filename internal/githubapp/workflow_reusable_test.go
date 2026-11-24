package githubapp

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

const reusableV1Fixture = `name: Publish
on: [push, pull_request, workflow_dispatch]
jobs:
  ship:
    runs-on: ubuntu-latest
    outputs:
      services_artifact: ${{ steps.services_capture.outputs.artifact }}
    steps:
      - id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/different/project
      - id: push
        uses: docker/build-push-action@v6
        with:
          push: true
          tags: ${{ steps.meta.outputs.tags }}
      - uses: everyonesoftware/workflows-common/actions/capture-image-v1@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
        id: services_capture
        with:
          digest: ${{ steps.push.outputs.digest }}
          tags: ${{ steps.meta.outputs.tags }}
          version: ${{ github.run_number }}
  deliver:
    needs: ship
    if: ${{ !cancelled() && (github.event_name == 'push' || github.event_name == 'workflow_dispatch') && github.ref == 'refs/heads/main' && needs.ship.result == 'success' && needs.ship.outputs.services_artifact != '' && vars.SERVICES_PUBLISH_NOTIFICATIONS == 'true' }}
    permissions:
      contents: read
      id-token: write
    uses: everyonesoftware/workflows-common/.github/workflows/notify-image-v1.yml@bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    with:
      artifact: ${{ needs.ship.outputs.services_artifact }}
      services-url: ${{ vars.SERVICES_PUBLISH_URL }}
      tailscale-client-id: ${{ vars.TAILSCALE_CLIENT_ID }}
      tailscale-audience: ${{ vars.TAILSCALE_AUDIENCE }}
      tailscale-tags: ${{ vars.TAILSCALE_TAGS }}
    secrets:
      services-token: ${{ secrets.SERVICES_PUBLISH_TOKEN }}
`

const reusableV1ScriptFixture = `name: Archive and publish
on:
  push: {branches: ['stable/releases']}
  workflow_dispatch:
jobs:
  select:
    runs-on: ubuntu-latest
    steps:
      - run: ./select-release.sh
  archive-publish:
    needs: select
    runs-on: ubuntu-24.04
    outputs: {services_artifact: '${{ steps.services_capture.outputs.artifact }}'}
    steps:
      - id: registry
        run: ./publish-archive.sh
      - id: services_capture
        if: steps.registry.outputs.digest != ''
        continue-on-error: true
        uses: everyonesoftware/workflows-common/actions/capture-image-v1@cccccccccccccccccccccccccccccccccccccccc
        with:
          digest: ${{ steps.registry.outputs.digest }}
          tags: |
            ghcr.io/unrelated/archived:latest
            ghcr.io/unrelated/archived:${{ steps.registry.outputs.version }}
          version: ${{ steps.registry.outputs.version }}
  notify:
    uses: everyonesoftware/workflows-common/.github/workflows/notify-image-v1.yml@cccccccccccccccccccccccccccccccccccccccc
    needs: [archive-publish]
    if: >-
      ${{ !cancelled() && (github.event_name == 'push' || github.event_name == 'workflow_dispatch') &&
      github.ref == 'refs/heads/stable/releases' && needs.archive-publish.result == 'success' &&
      needs.archive-publish.outputs.services_artifact != '' && vars.SERVICES_PUBLISH_NOTIFICATIONS == 'true' }}
    permissions: {contents: read, id-token: write}
    with:
      artifact: ${{ needs.archive-publish.outputs.services_artifact }}
      services-url: ${{ vars.SERVICES_PUBLISH_URL }}
      tailscale-client-id: ${{ vars.TAILSCALE_CLIENT_ID }}
      tailscale-audience: ${{ vars.TAILSCALE_AUDIENCE }}
      tailscale-tags: ${{ vars.TAILSCALE_TAGS }}
    secrets: {services-token: '${{ secrets.SERVICES_PUBLISH_TOKEN }}'}
`

func reusableV1Client(t *testing.T, repo Repository, files map[string]string) *Client {
	t.Helper()
	sha := baseSHA
	fallback := planTransport(t, files, &sha, nil)
	return fakeClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/repos/"+repo.FullName {
			return response(200, map[string]any{"id": repo.ID, "full_name": repo.FullName, "default_branch": repo.DefaultBranch}), nil
		}
		if r.Method == "GET" && r.URL.Path == "/repos/"+repo.FullName+"/git/ref/heads/"+repo.DefaultBranch {
			return response(200, map[string]any{"ref": "refs/heads/" + repo.DefaultBranch, "object": map[string]string{"type": "commit", "sha": sha}}), nil
		}
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/contents/"+senderPath) && files[senderPath] == "" {
			t.Fatal("reusable workflows should not need to read a local sender")
		}
		if r.Method == "GET" && r.URL.Query().Get("ref") == repo.DefaultBranch {
			path := strings.TrimPrefix(r.URL.Path, "/repos/"+repo.FullName+"/contents/")
			if content, ok := files[path]; ok {
				return response(200, map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))}), nil
			}
			return response(404, nil), nil
		}
		r = r.Clone(r.Context())
		r.URL.Path = strings.Replace(r.URL.Path, "/repos/"+repo.FullName+"/", "/repos/owner/repo/", 1)
		return fallback(r)
	})
}

func TestReusableV1RejectsUnsafeDeclarations(t *testing.T) {
	app, _ := testApp(t)
	for _, tc := range []struct{ name, old, replacement string }{
		{"wrong owner", "everyonesoftware/workflows-common/actions/", "lookalike/workflows-common/actions/"},
		{"mutable action", "@" + baseSHA, "@v1"},
		{"uppercase caller pin", "@" + treeSHA, "@" + strings.ToUpper(treeSHA)},
		{"missing capture", "actions/capture-image-v1", "actions/some-other-action"},
		{"wrong job output", "steps.services_capture.outputs.artifact", "steps.push.outputs.artifact"},
		{"wrong dependency", "needs: ship", "needs: [ship, another]"},
		{"wrong branch", "'refs/heads/main'", "'refs/heads/dev'"},
		{"missing cancellation gate", "!cancelled() && ", ""},
		{"unsafe trigger", "pull_request,", "pull_request_target,"},
		{"overprivileged", "contents: read", "contents: write"},
		{"wrong artifact input", "artifact: ${{ needs.ship.outputs.services_artifact }}", "artifact: ${{ needs.ship.outputs.unrelated }}"},
		{"wrong variables", "vars.TAILSCALE_TAGS", "vars.OTHER_TAGS"},
		{"escaped legacy sender", "      - id: push", `      - run: "node scripts/publish-\x6eotification.mjs send"` + "\n      - id: push"},
		{"extra secret", "      services-token:", "      extra: ${{ secrets.OTHER }}\n      services-token:"},
		{"inherited secrets", "secrets:\n      services-token: ${{ secrets.SERVICES_PUBLISH_TOKEN }}", "secrets: inherit"},
		{"invalid version", "${{ github.run_number }}", "0"},
		{"invalid tag", "          tags: ${{ steps.meta.outputs.tags }}\n          version:", "          tags: latest\n          version:"},
		{"misplaced extra uses", "      - id: push", "      - uses: everyonesoftware/workflows-common/.github/workflows/notify-image-v1.yml@" + treeSHA + "\n      - id: push"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(reusableV1Fixture, tc.old) {
				t.Fatal("fixture mutation did not match")
			}
			files := map[string]string{
				".github/workflows/verify-and-publish.yml": strings.Replace(reusableV1Fixture, tc.old, tc.replacement, 1),
				connectionPath: connectionTemplate, callbackPath: callbackTemplate,
			}
			c := reusableV1Client(t, repository, files)
			plan, err := c.PlanWorkflow(context.Background(), app, repository)
			if err != nil || plan.Supported || len(plan.Files) != 0 || len(plan.Problems) == 0 {
				t.Errorf("unsafe workflow became approvable: %+v %v", plan, err)
			}
			inspection, err := c.Inspect(context.Background(), app, repository)
			if err != nil || inspection.Supported || len(inspection.Problems) == 0 {
				t.Errorf("unsafe workflow passed inspection: %+v %v", inspection, err)
			}
		})
	}
}

func TestReusableV1RejectsMultipleHooks(t *testing.T) {
	app, _ := testApp(t)
	legacy, err := provisionConventionalWorkflow(conventionalWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	for name, extra := range map[string]string{"reusable": reusableV1Fixture, "legacy": legacy} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{
				".github/workflows/verify-and-publish.yml": reusableV1Fixture,
				".github/workflows/another.yml":            extra,
				connectionPath:                             connectionTemplate, callbackPath: callbackTemplate,
			}
			c := reusableV1Client(t, repository, files)
			plan, err := c.PlanWorkflow(context.Background(), app, repository)
			if err != nil || plan.Supported || len(plan.Files) != 0 || len(plan.Problems) != 1 || !strings.Contains(plan.Problems[0], "Multiple") {
				t.Errorf("ambiguous plan: %+v %v", plan, err)
			}
			inspection, err := c.Inspect(context.Background(), app, repository)
			if err != nil || inspection.Supported || len(inspection.Problems) != 1 || !strings.Contains(inspection.Problems[0], "Multiple") {
				t.Errorf("ambiguous installed hook: %+v %v", inspection, err)
			}
		})
	}
}

func TestReusableV1PlanAndInspect(t *testing.T) {
	app, _ := testApp(t)
	for _, tc := range []struct{ path, repo, branch, workflow string }{
		{".github/workflows/verify-and-publish.yml", "different/project", "main", reusableV1Fixture},
		{".github/workflows/images.yaml", "unrelated/archive", "stable/releases", reusableV1ScriptFixture},
	} {
		t.Run(tc.repo, func(t *testing.T) {
			repo := repository
			repo.FullName, repo.DefaultBranch = tc.repo, tc.branch
			files := map[string]string{tc.path: tc.workflow}
			if tc.path != ".github/workflows/verify-and-publish.yml" {
				files[".github/workflows/verify-and-publish.yml"] = "on: pull_request\njobs:\n  check:\n    runs-on: ubuntu-latest\n    steps:\n      - run: ./check.sh\n"
			}
			c := reusableV1Client(t, repo, files)
			plan, err := c.PlanWorkflow(context.Background(), app, repo)
			if err != nil || !plan.Supported || len(plan.Files) != 2 || len(plan.Fingerprint) != 64 {
				t.Fatalf("expected only connection-test setup: %v %v %d", err, plan.Problems, len(plan.Files))
			}
			pending, err := c.Inspect(context.Background(), app, repo)
			if err != nil || pending.Supported || len(pending.Problems) == 0 {
				t.Fatalf("pending connection test reported as installed: %+v %v", pending, err)
			}
			for _, f := range plan.Files {
				if f.Before != "" || (f.Path != connectionPath && f.Path != callbackPath) {
					t.Fatalf("unexpected edit: %s", f.Path)
				}
				files[f.Path] = f.After
			}
			again, err := c.PlanWorkflow(context.Background(), app, repo)
			if err != nil || !again.Supported || len(again.Files) != 0 {
				t.Fatalf("expected zero edits after applying plan: %+v %v", again, err)
			}
			installed, err := c.Inspect(context.Background(), app, repo)
			if err != nil || !installed.Supported || len(installed.Problems) != 0 {
				t.Fatalf("installed hook not recognized: %+v %v", installed, err)
			}
			files[senderPath] = "// a retained, unrelated local file"
			removal, err := c.PlanWorkflowRemoval(context.Background(), app, repo)
			if err != nil || removal.Supported || len(removal.Files) != 0 || len(removal.Problems) == 0 || !strings.Contains(removal.Problems[0], "reusable v1") {
				t.Fatalf("reusable hook must require manual removal: %+v %v", removal, err)
			}
		})
	}
}
