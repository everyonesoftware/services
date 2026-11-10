package githubapp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parsing is structural, not substring-based. Editing is deliberately narrower:
// retain original source bytes and insert only in unambiguous two-space block
// mappings. Aliases, duplicate keys, matrices and custom execution environments
// need manual review rather than speculative workflow changes.
func workflowYAML(text string) (*yaml.Node, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(text))
	if err := decoder.Decode(&doc); err != nil {
		return nil, errors.New("Workflow YAML is invalid.")
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("Multiple workflow YAML documents are unsupported.")
	}
	count := 0
	var validate func(*yaml.Node) error
	validate = func(n *yaml.Node) error {
		count++
		if count > 15000 || n.Kind == yaml.AliasNode || n.Anchor != "" {
			return errors.New("Workflow aliases or excessive complexity require manual review.")
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Kind != yaml.ScalarNode || seen[k.Value] || k.Value == "<<" {
					return errors.New("Duplicate or complex YAML keys require manual review.")
				}
				seen[k.Value] = true
			}
		}
		for _, c := range n.Content {
			if err := validate(c); err != nil {
				return err
			}
		}
		return nil
	}
	if err := validate(&doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("Workflow must be a YAML mapping.")
	}
	return doc.Content[0], nil
}
func yamlField(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
func yamlValue(n *yaml.Node, key string) string {
	v := yamlField(n, key)
	if v == nil || v.Kind != yaml.ScalarNode {
		return ""
	}
	return v.Value
}
func action(n *yaml.Node, prefix string) bool {
	s := yamlValue(n, "uses")
	return strings.HasPrefix(s, prefix+"@") && len(s) > len(prefix)+1
}
func buildPushCount(root *yaml.Node) int {
	jobs := yamlField(root, "jobs")
	count := 0
	if jobs == nil {
		return 0
	}
	for i := 1; i < len(jobs.Content); i += 2 {
		steps := yamlField(jobs.Content[i], "steps")
		if steps == nil {
			continue
		}
		for _, step := range steps.Content {
			if action(step, "docker/build-push-action") {
				count++
			}
		}
	}
	return count
}

func provisionConventionalWorkflow(text string) (string, error) {
	fail := func(reason string) (string, error) {
		return "", errors.New(reason + " Review the publication workflow manually.")
	}
	root, err := workflowYAML(text)
	if err != nil {
		return "", err
	}
	if err := validatePublicationEvents(root); err != nil {
		return "", err
	}
	jobs := yamlField(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode || buildPushCount(root) != 1 {
		return fail("Expected exactly one Docker build-push-action step.")
	}
	if root.Content[len(root.Content)-2].Value != "jobs" {
		return fail("Automatic source-preserving edits require jobs to be the final workflow mapping.")
	}
	if yamlField(root, "defaults") != nil || yamlField(root, "concurrency") != nil {
		return fail("Custom workflow defaults or concurrency cannot be safely adapted automatically.")
	}
	var job, steps, push *yaml.Node
	jobID := ""
	pushIndex := 0
	for i := 0; i < len(jobs.Content); i += 2 {
		candidate := yamlField(jobs.Content[i+1], "steps")
		if candidate == nil {
			continue
		}
		for j, step := range candidate.Content {
			if action(step, "docker/build-push-action") {
				job = jobs.Content[i+1]
				jobID = jobs.Content[i].Value
				steps = candidate
				push = step
				pushIndex = j
			}
		}
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(jobID) || jobID == "services_notify" || yamlField(jobs, "services_notify") != nil {
		return fail("The notification job name conflicts with an existing job.")
	}
	for _, key := range []string{"strategy", "container", "services", "defaults", "uses"} {
		if yamlField(job, key) != nil {
			return fail("Custom job execution or matrix publication is unsupported.")
		}
	}
	if yamlValue(job, "runs-on") != "ubuntu-latest" {
		return fail("Only conventional ubuntu-latest publication jobs are supported.")
	}
	if yamlField(push, "if") != nil || yamlField(push, "continue-on-error") != nil || yamlValue(yamlField(push, "with"), "push") != "true" {
		return fail("Conditional or non-pushing Docker steps are ambiguous.")
	}
	if yamlField(push, "env") != nil {
		return fail("Push-step environment overrides could change the captured version.")
	}
	pushID := yamlValue(push, "id")
	addPushID := pushID == ""
	if addPushID {
		pushID = "services_push"
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(pushID) {
		return fail("The Docker push step needs an explicit unique id.")
	}
	ids := map[string]bool{}
	checkedOut := false
	for i, step := range steps.Content {
		if yamlField(step, "working-directory") != nil {
			return fail("Custom step working directories are unsupported.")
		}
		id := yamlValue(step, "id")
		if id != "" {
			if ids[id] || id == "services_capture" || (addPushID && id == pushID) {
				return fail("Step identifiers conflict.")
			}
			ids[id] = true
		}
		if i < pushIndex && action(step, "actions/checkout") {
			checkout := yamlField(step, "with")
			ref := yamlValue(checkout, "ref")
			if yamlValue(checkout, "path") != "" || (ref != "" && ref != "${{ github.sha }}") || yamlValue(checkout, "repository") != "" {
				return fail("A custom checkout cannot prove the publication's original repository and commit.")
			}
			checkedOut = true
		}
	}
	if !checkedOut {
		return fail("Publication must check out repository scripts before pushing.")
	}
	with := yamlField(push, "with")
	tags := yamlValue(with, "tags")
	if context := yamlValue(with, "context"); context != "" && context != "." {
		return fail("A custom Docker build context cannot prove publication provenance.")
	}
	metaMatch := regexp.MustCompile(`^\$\{\{\s*steps\.([A-Za-z_][A-Za-z0-9_]*)\.outputs\.tags\s*\}\}$`).FindStringSubmatch(tags)
	ghcr := false
	if metaMatch != nil {
		for i, step := range steps.Content {
			if i < pushIndex && yamlValue(step, "id") == metaMatch[1] && action(step, "docker/metadata-action") {
				image := yamlValue(yamlField(step, "with"), "images")
				ghcr = regexp.MustCompile(`^ghcr\.io/(\$\{\{\s*github\.repository\s*\}\}|[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*)$`).MatchString(image)
			}
		}
	} else {
		// Docker accepts newline/CSV lists. Preserve the evaluated references,
		// normalizing only the input separators to the sender's newline format.
		references := strings.FieldsFunc(tags, func(r rune) bool { return r == '\n' || r == ',' })
		image := ""
		tagLines := []string{}
		ghcr = len(references) > 0
		for _, reference := range references {
			reference = strings.TrimSpace(reference)
			colon := strings.LastIndex(reference, ":")
			if colon < 0 {
				ghcr = false
				break
			}
			name, tag := reference[:colon], reference[colon+1:]
			if !regexp.MustCompile(`^ghcr\.io/(\$\{\{\s*github\.repository\s*\}\}|[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*)$`).MatchString(name) || (image != "" && name != image) || !regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_.-]*|\$\{\{\s*(github\.run_number|steps\.[A-Za-z_][A-Za-z0-9_]*\.outputs\.[A-Za-z_][A-Za-z0-9_]*|needs\.[A-Za-z_][A-Za-z0-9_]*\.outputs\.[A-Za-z_][A-Za-z0-9_]*|env\.[A-Za-z_][A-Za-z0-9_]*)\s*\}\})$`).MatchString(tag) {
				ghcr = false
				break
			}
			image = name
			tagLines = append(tagLines, "            "+reference)
		}
		tags = "|-\n" + strings.Join(tagLines, "\n")
	}
	if !ghcr {
		return fail("Only a single unambiguous GHCR image is supported.")
	}
	version := ""
	for _, line := range strings.Split(yamlValue(with, "build-args"), "\n") {
		if strings.HasPrefix(line, "APP_VERSION=") {
			if version != "" {
				return fail("APP_VERSION is duplicated.")
			}
			version = strings.TrimPrefix(line, "APP_VERSION=")
		}
	}
	if !regexp.MustCompile(`^([1-9][0-9]*|\$\{\{\s*(github\.run_number|needs\.[A-Za-z_][A-Za-z0-9_]*\.outputs\.[A-Za-z_][A-Za-z0-9_]*|steps\.[A-Za-z_][A-Za-z0-9_]*\.outputs\.[A-Za-z_][A-Za-z0-9_]*|env\.[A-Za-z_][A-Za-z0-9_]*)\s*\}\})$`).MatchString(version) {
		return fail("A numeric APP_VERSION build argument with an existing single source of truth is required.")
	}
	// Our source-preserving insertions require block nodes with conventional indent.
	if regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(version) {
		version = strconv.Quote(version)
	}
	if job.Column != 5 || push.Column != 9 || jobs.Column != 3 || strings.Contains(text, "\r") {
		return fail("Nonconventional YAML indentation or line endings are unsupported.")
	}
	lines := strings.Split(text, "\n")
	pushStart := push.Line - 1
	// Mapping node starts after '- '; first property can be uses or name.
	if pushStart < 0 || !strings.HasPrefix(lines[pushStart], "      - ") {
		return fail("Flow-style workflow steps are unsupported.")
	}
	pushEnd := len(lines)
	if pushIndex+1 < len(steps.Content) {
		pushEnd = steps.Content[pushIndex+1].Line - 1
	} else {
		for i := pushStart + 1; i < len(lines); i++ {
			line := lines[i]
			if strings.TrimSpace(line) != "" && len(line)-len(strings.TrimLeft(line, " ")) <= 4 && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				pushEnd = i
				break
			}
		}
	}
	outputs := yamlField(job, "outputs")
	outputLine := job.Line - 1
	outputText := "    outputs:\n      services_artifact: ${{ steps.services_capture.outputs.artifact }}\n"
	if outputs != nil {
		if outputs.Kind != yaml.MappingNode || outputs.Style != 0 || yamlField(outputs, "services_artifact") != nil {
			return fail("Existing job outputs cannot be safely extended.")
		}
		outputLine = outputs.Line - 1
		outputText = "      services_artifact: ${{ steps.services_capture.outputs.artifact }}\n"
	}
	capture := fmt.Sprintf(`
      - name: Capture original Services publication metadata
        id: services_capture
        continue-on-error: true
        env:
          PUBLISHED_DIGEST: ${{ steps.%s.outputs.digest }}
          PUBLISHED_TAGS: %s
          APP_VERSION: %s
          SERVICES_PUBLISH_PAYLOAD_PATH: ${{ runner.temp }}/services-publish/published.json
        run: node scripts/publish-notification.mjs capture

      - name: Retain original Services payload for notification-only retries
        if: steps.services_capture.outcome == 'success'
        continue-on-error: true
        uses: actions/upload-artifact@v7
        with:
          name: ${{ steps.services_capture.outputs.artifact }}
          path: ${{ runner.temp }}/services-publish/published.json
          if-no-files-found: error
          retention-days: 30
          overwrite: false
          include-hidden-files: false
          archive: true

`, pushID, tags, version)
	setup := "      - name: Set up Node.js for Services payload capture\n        uses: actions/setup-node@v7\n        with:\n          node-version: '24'\n\n"
	insertions := map[int]string{}
	insertions[pushEnd] += capture
	insertions[pushStart] += setup
	if addPushID {
		insertions[pushStart+1] += "        id: " + pushID + "\n"
	}
	insertions[outputLine] += outputText
	// Ensure all workflow runs participating in mutable-tag publication serialize.
	insertions[jobs.Line-2] += "concurrency:\n  group: services-publish-${{ github.repository }}\n  cancel-in-progress: false\n  queue: max\n\n"
	var out bytes.Buffer
	for i, line := range lines {
		out.WriteString(insertions[i])
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	out.WriteString(insertions[len(lines)])
	out.WriteString(strings.ReplaceAll(fmt.Sprintf(notifyTemplate, jobID, jobID, jobID), "REPLACE_JOB", jobID))
	result := out.String()
	if err := validateNotificationHook(result); err != nil {
		return fail("Generated workflow could not be validated: " + err.Error())
	}
	return result, nil
}

const notifyTemplate = `
  services_notify:
    name: Notify Services (publication remains separate)
    needs: %s
    if: (github.event_name == 'push' || github.event_name == 'workflow_dispatch' || github.event_name == 'release') && needs.%s.result == 'success' && vars.SERVICES_PUBLISH_NOTIFICATIONS == 'true'
    runs-on: ubuntu-latest
    timeout-minutes: 10
    permissions:
      contents: read
      id-token: write
    steps:
      - uses: actions/checkout@v6
        with:
          persist-credentials: false
      - uses: actions/setup-node@v7
        with:
          node-version: '24'
      - name: Require original payload artifact
        env:
          PAYLOAD_ARTIFACT: ${{ needs.%s.outputs.services_artifact }}
        run: test -n "$PAYLOAD_ARTIFACT"
      - uses: actions/download-artifact@v8
        with:
          name: ${{ needs.REPLACE_JOB.outputs.services_artifact }}
          path: ${{ runner.temp }}/services-publish
          digest-mismatch: error
      - uses: tailscale/github-action@v4
        with:
          oauth-client-id: ${{ vars.TAILSCALE_CLIENT_ID }}
          audience: ${{ vars.TAILSCALE_AUDIENCE }}
          tags: ${{ vars.TAILSCALE_TAGS }}
          log-mode: quiet
      - name: Send the retained original payload without rebuilding
        env:
          SERVICES_PUBLISH_URL: ${{ vars.SERVICES_PUBLISH_URL }}
          SERVICES_PUBLISH_TOKEN: ${{ secrets.SERVICES_PUBLISH_TOKEN }}
          SERVICES_PUBLISH_PAYLOAD_PATH: ${{ runner.temp }}/services-publish/published.json
        run: node scripts/publish-notification.mjs send
`
