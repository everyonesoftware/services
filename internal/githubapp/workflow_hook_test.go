package githubapp

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Exercise both public compatibility paths against the actual checked-in hook.
func TestExistingHookRejectsMissingDigestProducer(t *testing.T) {
	files := fixtureFiles(t)
	path := ".github/workflows/verify-and-publish.yml"
	files[path] = strings.Replace(files[path], "PUBLISHED_DIGEST: ${{ steps.push.outputs.digest }}", "PUBLISHED_DIGEST: ${{ steps.missing.outputs.digest }}", 1)
	assertHookCompatibility(t, files, false)
}

func TestHookRequiresOriginalCaptureBindings(t *testing.T) {
	fixtures := fixtureFiles(t)
	existing := strings.ReplaceAll(fixtures[".github/workflows/verify-and-publish.yml"], "\r\n", "\n")
	generated, err := provisionConventionalWorkflow(conventionalWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []struct{ name, text, capture, output string }{
		{"existing", existing, "capture", "notification_artifact"},
		{"generated", generated, "services_capture", "services_artifact"},
	} {
		t.Run(layout.name, func(t *testing.T) {
			version := "needs.verify.outputs.version"
			if layout.name == "generated" {
				version = "github.run_number"
			}
			for _, tc := range []struct{ name, old, replacement string }{
				{"wrong digest output", "PUBLISHED_DIGEST: ${{ steps.push.outputs.digest }}", "PUBLISHED_DIGEST: ${{ steps.meta.outputs.digest }}"},
				{"missing digest", "PUBLISHED_DIGEST: ${{ steps.push.outputs.digest }}", "OTHER_DIGEST: unused"},
				{"non-pushing build", "push: true", "push: false"},
				{"conditional push", "        id: push", "        if: false\n        id: push"},
				{"ignored push failure", "        id: push", "        continue-on-error: true\n        id: push"},
				{"wrong tags", "PUBLISHED_TAGS: ${{ steps.meta.outputs.tags }}", "PUBLISHED_TAGS: ghcr.io/other/image:latest"},
				{"missing tag producer", "id: meta", "id: unrelated"},
				{"wrong version", "APP_VERSION: ${{ " + version + " }}", "APP_VERSION: '999'"},
				{"wrong upload output", "name: ${{ steps." + layout.capture + ".outputs.artifact }}", "name: ${{ steps.missing.outputs.artifact }}"},
				{"wrong upload guard", "if: steps." + layout.capture + ".outcome == 'success'", "if: always()"},
				{"wrong upload path", "path: ${{ runner.temp }}/services-publish/published.json", "path: ${{ runner.temp }}/other.json"},
				{"wrong job output", layout.output + ": ${{ steps." + layout.capture + ".outputs.artifact }}", layout.output + ": ${{ steps.missing.outputs.artifact }}"},
				{"wrong download output", "name: ${{ needs.publish.outputs." + layout.output + " }}", "name: ${{ needs.publish.outputs.missing }}"},
				{"wrong download path", "path: ${{ runner.temp }}/services-publish\n", "path: ${{ runner.temp }}/elsewhere\n"},
				{"missing notify need", "    needs: publish\n", "    needs: verify\n"},
				{"missing successful producer guard", "needs.publish.result == 'success'", "needs.publish.result != 'cancelled'"},
				{"wrong sender path", "SERVICES_PUBLISH_PAYLOAD_PATH: ${{ runner.temp }}/services-publish/published.json\n        run: node scripts/publish-notification.mjs send", "SERVICES_PUBLISH_PAYLOAD_PATH: ${{ runner.temp }}/other.json\n        run: node scripts/publish-notification.mjs send"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if !strings.Contains(layout.text, tc.old) {
						t.Fatal("mutation did not match")
					}
					files := fixtureFiles(t)
					files[".github/workflows/verify-and-publish.yml"] = strings.Replace(layout.text, tc.old, tc.replacement, 1)
					assertHookCompatibility(t, files, false)
				})
			}
			t.Run("capture before push", func(t *testing.T) {
				start := strings.Index(layout.text, "      - name: Capture")
				end := strings.Index(layout.text[start:], "      - name: ")
				// The next step starts after the capture step's run command.
				end = strings.Index(layout.text[start+len("      - name: "):], "      - name: ") + start + len("      - name: ")
				capture := layout.text[start:end]
				text := layout.text[:start] + layout.text[end:]
				push := strings.Index(text, "      - name: Build and publish image")
				if layout.name == "generated" {
					push = strings.Index(text, "      - uses: docker/build-push-action")
				}
				files := fixtureFiles(t)
				files[".github/workflows/verify-and-publish.yml"] = text[:push] + capture + text[push:]
				assertHookCompatibility(t, files, false)
			})
		})
	}
}

func TestHookRejectsIncompleteOrCustomStructuralChains(t *testing.T) {
	existing := fixtureFiles(t)[".github/workflows/verify-and-publish.yml"]
	generated, err := provisionConventionalWorkflow(conventionalWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []struct{ name, text string }{{"existing", existing}, {"generated", generated}} {
		for _, tc := range []struct {
			name   string
			mutate func(*yaml.Node)
		}{
			{"conditional metadata", func(root *yaml.Node) { setHookField(hookStep(root, "publish", "meta"), "if", "false") }},
			{"missing version source", func(root *yaml.Node) {
				push := hookStep(root, "publish", "push")
				capture := hookCapture(root)
				setHookField(yamlField(push, "with"), "build-args", "APP_VERSION=${{ steps.missing.outputs.version }}")
				setHookField(yamlField(capture, "env"), "APP_VERSION", "${{ steps.missing.outputs.version }}")
			}},
			{"custom capture shell", func(root *yaml.Node) { setHookField(hookCapture(root), "shell", "echo {0}") }},
			{"foreign publication checkout", func(root *yaml.Node) {
				ss := yamlField(yamlField(yamlField(root, "jobs"), "publish"), "steps")
				for _, step := range ss.Content {
					if action(step, "actions/checkout") {
						w := &yaml.Node{Kind: yaml.MappingNode}
						setHookField(w, "repository", "other/project")
						step.Content = append(step.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "with"}, w)
						return
					}
				}
			}},
			{"remote build context", func(root *yaml.Node) {
				setHookField(yamlField(hookStep(root, "publish", "push"), "with"), "context", "https://github.com/other/project.git")
			}},
			{"wrong federation variable", func(root *yaml.Node) {
				for _, job := range yamlField(root, "jobs").Content {
					ss := yamlField(job, "steps")
					if ss == nil {
						continue
					}
					for _, step := range ss.Content {
						if action(step, "tailscale/github-action") {
							setHookField(yamlField(step, "with"), "tags", "tag:unrelated")
						}
					}
				}
			}},
			{"alter retained payload after download", func(root *yaml.Node) {
				for _, job := range yamlField(root, "jobs").Content {
					ss := yamlField(job, "steps")
					if ss == nil {
						continue
					}
					for i, step := range ss.Content {
						if action(step, "actions/download-artifact") {
							extra := &yaml.Node{Kind: yaml.MappingNode}
							setHookField(extra, "run", "echo corrupted > $RUNNER_TEMP/services-publish/published.json")
							ss.Content = append(ss.Content[:i+1], append([]*yaml.Node{extra}, ss.Content[i+1:]...)...)
							return
						}
					}
				}
			}},
		} {
			t.Run(layout.name+"/"+tc.name, func(t *testing.T) {
				root, err := workflowYAML(layout.text)
				if err != nil {
					t.Fatal(err)
				}
				before, err := yaml.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				tc.mutate(root)
				after, err := yaml.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				if string(before) == string(after) {
					t.Fatal("mutation did not apply")
				}
				files := fixtureFiles(t)
				files[".github/workflows/verify-and-publish.yml"] = string(after)
				assertHookCompatibility(t, files, false)
			})
		}
	}
}

func TestHookRejectsDownloadProvenanceOverrides(t *testing.T) {
	existing := fixtureFiles(t)[".github/workflows/verify-and-publish.yml"]
	generated, err := provisionConventionalWorkflow(conventionalWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []struct{ name, text string }{{"existing", existing}, {"generated", generated}} {
		t.Run(layout.name, func(t *testing.T) {
			files := fixtureFiles(t)
			files[".github/workflows/verify-and-publish.yml"] = layout.text
			assertHookCompatibility(t, files, true)
			for _, tc := range []struct{ key, value string }{
				{"run-id", "12345"},
				{"repository", "other/project"},
				{"github-token", "${{ secrets.OTHER_TOKEN }}"},
				{"artifact-ids", "12345"},
				{"pattern", "*"},
				{"merge-multiple", "true"},
				{"unknown-input", "unexpected"},
				{"scalar-with", ""},
				{"sequence-with", ""},
			} {
				t.Run(tc.key, func(t *testing.T) {
					root, err := workflowYAML(layout.text)
					if err != nil {
						t.Fatal(err)
					}
					before, err := yaml.Marshal(root)
					if err != nil {
						t.Fatal(err)
					}
					for _, job := range yamlField(root, "jobs").Content {
						steps := yamlField(job, "steps")
						if steps == nil {
							continue
						}
						for _, step := range steps.Content {
							if !action(step, "actions/download-artifact") {
								continue
							}
							with := yamlField(step, "with")
							switch tc.key {
							case "scalar-with":
								*with = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "unexpected"}
							case "sequence-with":
								*with = yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "unexpected"}}}
							default:
								setHookField(with, tc.key, tc.value)
							}
						}
					}
					after, err := yaml.Marshal(root)
					if err != nil {
						t.Fatal(err)
					}
					if string(before) == string(after) {
						t.Fatal("mutation did not apply")
					}
					files := fixtureFiles(t)
					files[".github/workflows/verify-and-publish.yml"] = string(after)
					assertHookCompatibility(t, files, false)
				})
			}
		})
	}
}

func setHookField(n *yaml.Node, key, value string) {
	if v := yamlField(n, key); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!str"
		v.Value = value
		return
	}
	n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}
func hookStep(root *yaml.Node, job, id string) *yaml.Node {
	for _, step := range yamlField(yamlField(yamlField(root, "jobs"), job), "steps").Content {
		if yamlValue(step, "id") == id {
			return step
		}
	}
	return nil
}
func hookCapture(root *yaml.Node) *yaml.Node {
	for _, step := range yamlField(yamlField(yamlField(root, "jobs"), "publish"), "steps").Content {
		if strings.TrimSpace(yamlValue(step, "run")) == "node scripts/publish-notification.mjs capture" {
			return step
		}
	}
	return nil
}

func TestExpressionNormalizationPreservesLiteralAndTokenBoundaries(t *testing.T) {
	for _, pair := range [][2]string{
		{"${{ steps.p ush.outputs.digest }}", "${{steps.push.outputs.digest}}"},
		{"${{ 'not safe' }}", "${{'notsafe'}}"},
		{"${{ 'it''s safe' }}", "${{'it''ssafe'}}"},
		{"ghcr.io/owner/image:my tag", "ghcr.io/owner/image:mytag"},
		{" ${{ runner.temp }}/services-publish/published.json ", "${{ runner.temp }}/services-publish/published.json"},
	} {
		if sameBinding(pair[0], pair[1]) {
			t.Errorf("unsafe normalization: %q == %q", pair[0], pair[1])
		}
	}
	if !sameBinding("${{ steps . push.outputs.digest }}", "${{steps.push.outputs.digest}}") {
		t.Fatal("expression whitespace not normalized")
	}
}

func TestHookRejectsNonMappingJobOutputsWithoutPanicking(t *testing.T) {
	root, err := workflowYAML(fixtureFiles(t)[".github/workflows/verify-and-publish.yml"])
	if err != nil {
		t.Fatal(err)
	}
	outputs := yamlField(yamlField(yamlField(root, "jobs"), "publish"), "outputs")
	outputs.Kind = yaml.SequenceNode
	outputs.Content = []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: "one"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: "two"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: "three"}}
	text, err := yaml.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNotificationHook(string(text)); err == nil {
		t.Fatal("invalid job outputs approved")
	}
}

func TestPublicationEventsAndGeneratedCredentialGuard(t *testing.T) {
	for _, event := range []string{"pull_request_target", "workflow_run", "repository_dispatch", "unknown"} {
		t.Run(event, func(t *testing.T) {
			workflow := strings.Replace(conventionalWorkflow, "  push:", "  "+event+":", 1)
			if _, err := provisionConventionalWorkflow(workflow); err == nil {
				t.Fatal("unsafe event accepted")
			}
			files := fixtureFiles(t)
			files[".github/workflows/verify-and-publish.yml"] = strings.Replace(files[".github/workflows/verify-and-publish.yml"], "  pull_request:", "  "+event+":", 1)
			assertHookCompatibility(t, files, false)
		})
	}
	generated, err := provisionConventionalWorkflow(conventionalWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(generated, "(github.event_name == 'push' || github.event_name == 'workflow_dispatch' || github.event_name == 'release') && needs.publish.result == 'success'") {
		t.Fatal("notify lacks explicit event allowlist")
	}
}

func TestHookAcceptsEquivalentScalarFormatting(t *testing.T) {
	files := fixtureFiles(t)
	path := ".github/workflows/verify-and-publish.yml"
	text := files[path]
	text = strings.ReplaceAll(text, "${{ steps.push.outputs.digest }}", "${{steps.push.outputs.digest}}")
	text = strings.ReplaceAll(text, "PUBLISHED_TAGS: ${{ steps.meta.outputs.tags }}", "PUBLISHED_TAGS: '${{steps.meta.outputs.tags}}'")
	text = strings.ReplaceAll(text, "needs: publish", "needs: [publish]")
	files[path] = text
	assertHookCompatibility(t, files, true)
}

func assertHookCompatibility(t *testing.T, files map[string]string, supported bool) {
	t.Helper()
	app, _ := testApp(t)
	sha := baseSHA
	fallback := planTransport(t, files, &sha, nil)
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/contents/") && r.URL.Query().Get("ref") == "main" {
			path := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo/contents/")
			content, ok := files[path]
			if !ok {
				return response(404, nil), nil
			}
			return response(200, map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(content))}), nil
		}
		return fallback(r)
	})
	plan, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || plan.Supported != supported || (!supported && (len(plan.Problems) == 0 || len(plan.Files) != 0)) {
		t.Errorf("PlanWorkflow: supported=%v problems=%v files=%d error=%v", plan.Supported, plan.Problems, len(plan.Files), err)
	}
	inspection, err := c.Inspect(context.Background(), app, repository)
	if err != nil || inspection.Supported != supported || (!supported && len(inspection.Problems) == 0) {
		t.Errorf("Inspect: %+v error=%v", inspection, err)
	}
}
