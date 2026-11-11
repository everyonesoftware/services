package githubapp

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConventionalHostedUbuntuAndCRLFPreserveSource(t *testing.T) {
	for _, runner := range []string{"ubuntu-latest", "ubuntu-22.04", "ubuntu-24.04"} {
		for _, eol := range []string{"\n", "\r\n"} {
			t.Run(runner+"/"+strings.ReplaceAll(eol, "\n", "LF"), func(t *testing.T) {
				original := strings.ReplaceAll(conventionalWorkflow, "ubuntu-latest", runner)
				original = strings.ReplaceAll(original, "${{ github.run_number }}", "${{github.run_number}}")
				original = strings.ReplaceAll(original, "\n", eol)
				after, err := provisionConventionalWorkflow(original)
				if err != nil {
					t.Fatal(err)
				}
				if eol == "\r\n" && strings.Contains(strings.ReplaceAll(after, "\r\n", ""), "\n") {
					t.Fatal("mixed line endings")
				}
				if !strings.Contains(after, "          build-args: APP_VERSION=${{github.run_number}}"+eol) {
					t.Fatal("build args rewritten")
				}
				if err := validateNotificationHook(after); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestConventionalVersionArgumentsShareOneSource(t *testing.T) {
	for _, args := range []string{
		"APP_VERSION=${{ github.run_number }}",
		"VERSION=${{ github.run_number }}",
		"BUILD_VERSION=${{ github.run_number }}",
		"APP_VERSION=${{ github.run_number }}\n            VERSION=${{github.run_number}}\n            BUILD_VERSION=${{ github.run_number }}\n            OTHER=value",
	} {
		t.Run(args, func(t *testing.T) {
			workflow := strings.ReplaceAll(conventionalWorkflow, "build-args: APP_VERSION=${{ github.run_number }}", "build-args: |-\n            "+args)
			after, err := provisionConventionalWorkflow(workflow)
			if err != nil {
				t.Fatal(err)
			}
			beforeRoot, _ := workflowYAML(workflow)
			afterRoot, _ := workflowYAML(after)
			getArgs := func(root *yaml.Node) string {
				steps := yamlField(yamlField(yamlField(root, "jobs"), "publish"), "steps")
				for _, step := range steps.Content {
					if action(step, "docker/build-push-action") {
						return yamlValue(yamlField(step, "with"), "build-args")
					}
				}
				return ""
			}
			if getArgs(beforeRoot) != getArgs(afterRoot) {
				t.Fatal("build arguments changed")
			}
			if err := validateNotificationHook(after); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConventionalVersionArgumentsRejectAmbiguityAndUnboundSources(t *testing.T) {
	for _, args := range []string{
		"OTHER=value",
		"VERSION=",
		"VERSION",
		"VERSION=0",
		"VERSION=1.2.3",
		"VERSION =12",
		"VERSION= 12",
		"VERSION=${{ steps.missing.outputs.version }}",
		"VERSION=${{ needs.missing.outputs.version }}",
		"VERSION=${{ env.MISSING }}",
		"VERSION=12\n            APP_VERSION=13",
		"APP_VERSION=12\n            VERSION=13",
		"BUILD_VERSION=12\n            VERSION=13",
		"VERSION=12\n            VERSION=12",
		"APP_VERSION=12\n            APP_VERSION=",
	} {
		t.Run(args, func(t *testing.T) {
			workflow := strings.ReplaceAll(conventionalWorkflow, "build-args: APP_VERSION=${{ github.run_number }}", "build-args: |-\n            "+args)
			if after, err := provisionConventionalWorkflow(workflow); err == nil {
				t.Fatalf("accepted ambiguous source: %s", after)
			}
		})
	}
}

func TestVersionArgumentsRejectUnsupportedCSV(t *testing.T) {
	base := strings.ReplaceAll(conventionalWorkflow, "APP_VERSION=${{ github.run_number }}", "VERSION=12")
	hook, err := provisionConventionalWorkflow(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNotificationHook(hook); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]string{
		"duplicate":                           "VERSION=12\n\"VERSION=13\"",
		"same value duplicate":                "VERSION=12\n\"VERSION=12\"",
		"app version conflict":                "VERSION=12\n\"APP_VERSION=13\"",
		"build version conflict":              "VERSION=12\n\"BUILD_VERSION=13\"",
		"quoted unknown arg":                  "VERSION=12\n\"OTHER=value\"",
		"quoted comma record":                 "VERSION=12\n\"VERSION=13\",OTHER=value",
		"unquoted comma record":               "VERSION=12\nOTHER=value,VERSION=13",
		"unknown quoted record hides version": "\"OTHER=value\nVERSION=13\"\nVERSION=12",
	} {
		args = "build-args: |-\n            " + strings.ReplaceAll(args, "\n", "\n            ")
		t.Run(name+"/provision", func(t *testing.T) {
			workflow := strings.Replace(base, "build-args: VERSION=12", args, 1)
			if _, err := provisionConventionalWorkflow(workflow); err == nil {
				t.Fatal("accepted unsupported CSV build arguments")
			}
		})
		t.Run(name+"/installed hook", func(t *testing.T) {
			workflow := strings.Replace(hook, "build-args: VERSION=12", args, 1)
			if err := validateNotificationHook(workflow); err == nil {
				t.Fatal("accepted unsupported CSV build arguments")
			}
		})
	}
}

func TestVersionAdapterKeepsSafetyBoundaries(t *testing.T) {
	data, err := os.ReadFile("testdata/agent-publish-image.yml")
	if err != nil {
		t.Fatal(err)
	}
	original := string(data)
	for name, workflow := range map[string]string{
		"missing dependency":      strings.Replace(original, "    needs: verify\n", "", 1),
		"conditional source job":  strings.Replace(original, "  verify:\n", "  verify:\n    if: false\n", 1),
		"conditional source step": strings.Replace(original, "        id: version\n", "        id: version\n        if: false\n", 1),
		"missing output":          strings.Replace(original, "version: ${{ steps.version.outputs.value }}", "other: ${{ steps.version.outputs.value }}", 1),
		"matrix":                  strings.Replace(original, "  publish:\n", "  publish:\n    strategy: {matrix: {arch: [amd64, arm64]}}\n", 1),
		"concurrency":             strings.Replace(original, "jobs:\n", "concurrency: custom\njobs:\n", 1),
		"privileged PR":           strings.Replace(original, "  workflow_dispatch:\n", "  pull_request_target:\n", 1),
		"self hosted":             strings.ReplaceAll(original, "ubuntu-latest", "self-hosted"),
		"runner labels":           strings.ReplaceAll(original, "runs-on: ubuntu-latest", "runs-on: [ubuntu-latest]"),
		"mixed endings":           strings.Replace(original, "name: Publish container image\n", "name: Publish container image\r\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := provisionConventionalWorkflow(workflow); err == nil {
				t.Fatal("unsafe workflow accepted")
			}
		})
	}
	after, err := provisionConventionalWorkflow(original)
	if err != nil {
		t.Fatal(err)
	}
	for name, workflow := range map[string]string{
		"capture drift":           strings.Replace(after, "APP_VERSION: ${{ needs.verify.outputs.version }}", "APP_VERSION: ${{ github.run_number }}", 1),
		"conflicting build arg":   strings.Replace(after, "build-args: VERSION=${{ needs.verify.outputs.version }}", "build-args: |-\n            VERSION=${{ needs.verify.outputs.version }}\n            BUILD_VERSION=${{ github.run_number }}", 1),
		"conditional source step": strings.Replace(after, "        id: version\n", "        id: version\n        if: false\n", 1),
		"privileged PR":           strings.Replace(after, "  workflow_dispatch:\n", "  pull_request_target:\n", 1),
		"custom runner":           strings.ReplaceAll(after, "ubuntu-latest", "self-hosted"),
	} {
		t.Run("hook/"+name, func(t *testing.T) {
			if err := validateNotificationHook(workflow); err == nil {
				t.Fatal("unsafe hook accepted")
			}
		})
	}
}

func TestMissingVersionWarningDescribesAdapterRequirements(t *testing.T) {
	workflow := strings.ReplaceAll(conventionalWorkflow, "APP_VERSION=${{ github.run_number }}", "OTHER=value")
	_, err := provisionConventionalWorkflow(workflow)
	if err == nil || !containsAll(err.Error(), "APP_VERSION", "VERSION", "BUILD_VERSION", "existing numeric version source", "manually") {
		t.Fatalf("%v", err)
	}
}

// Read verbatim with gh api from everyonesoftware/agent; blob
// 9a89e76b6f888f51eddf78cd3d607e9fef90bc86.
func TestAgentWorkflowVersionPlanValidateRepeatInspect(t *testing.T) {
	data, err := os.ReadFile("testdata/agent-publish-image.yml")
	if err != nil {
		t.Fatal(err)
	}
	for name, workflow := range map[string]string{
		"verbatim":           string(data),
		"CRLF":               strings.ReplaceAll(string(data), "\n", "\r\n"),
		"ubuntu-22.04":       strings.ReplaceAll(string(data), "ubuntu-latest", "ubuntu-22.04"),
		"ubuntu-24.04":       strings.ReplaceAll(string(data), "ubuntu-latest", "ubuntu-24.04"),
		"expression spacing": strings.ReplaceAll(string(data), "${{ needs.verify.outputs.version }}", "${{needs.verify.outputs.version}}"),
	} {
		t.Run(name, func(t *testing.T) { checkAgentWorkflowVersionRoundTrip(t, workflow) })
	}
}

func checkAgentWorkflowVersionRoundTrip(t *testing.T, workflow string) {
	t.Helper()
	app, _ := testApp(t)
	sha := baseSHA
	path := ".github/workflows/publish-image.yml"
	files := map[string]string{path: workflow}
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || !plan.Supported || len(plan.Files) != 4 {
		t.Fatalf("%+v %v", plan, err)
	}
	for _, f := range plan.Files {
		files[f.Path] = f.After
	}
	if !strings.Contains(scalarBinding(files[path]), "build-args: VERSION=${{needs.verify.outputs.version}}") || !strings.Contains(scalarBinding(files[path]), "APP_VERSION: ${{needs.verify.outputs.version}}") {
		t.Fatal("original version source changed")
	}
	if err := validateNotificationHook(files[path]); err != nil {
		t.Fatal(err)
	}
	again, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || !again.Supported || len(again.Files) != 0 {
		t.Fatalf("%+v %v", again, err)
	}
	fallback := planTransport(t, files, &sha, nil)
	c = fakeClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/contents/") && r.URL.Query().Get("ref") == "main" {
			p := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/contents/")
			s, ok := files[p]
			if !ok {
				return response(404, nil), nil
			}
			return response(200, map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(s))}), nil
		}
		return fallback(r)
	})
	inspection, err := c.Inspect(context.Background(), app, repository)
	if err != nil || !inspection.Supported || len(inspection.Problems) != 0 {
		t.Fatalf("%+v %v", inspection, err)
	}
}
