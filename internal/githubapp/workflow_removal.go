package githubapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// PlanWorkflowRemoval reads the actual default branch at an immutable commit.
// Supported with no Files confirms absence of active Services workflow hooks;
// retained repository scripts are deliberately not deleted.
func (c *Client) PlanWorkflowRemoval(ctx context.Context, app App, repo Repository) (WorkflowPlan, error) {
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repo, map[string]string{"metadata": "read", "contents": "read"})
	if err != nil {
		return WorkflowPlan{}, err
	}
	s, err := o.planWorkflowKind(repo, token, true)
	return s.plan, err
}

func planRemovalSnapshot(repo Repository, s workflowSnapshot, paths []string, files map[string]string) (workflowSnapshot, error) {
	for _, path := range paths {
		before := files[path]
		after, err := removeWorkflowHook(before)
		if path == connectionPath {
			if strings.ReplaceAll(before, "\r\n", "\n") == connectionTemplate {
				after, err = "", nil
			} else {
				err = errors.New("The connection-test workflow is custom; review it manually.")
			}
		}
		if err != nil {
			s.plan.Problems = append(s.plan.Problems, path+": "+err.Error())
			s.plan.Files = []WorkflowFile{}
			return s, nil
		}
		if after != before {
			s.plan.Files = append(s.plan.Files, WorkflowFile{Path: path, Before: before, After: after})
		}
	}
	s.plan.Supported = true
	encoded, _ := json.Marshal(struct {
		Operation  string
		Repository Repository
		Base, Tree string
		Files      map[string]string
		Changes    []WorkflowFile
	}{"removal", repo, s.sha, s.tree, files, s.plan.Files})
	digest := sha256.Sum256(encoded)
	s.plan.Fingerprint = hex.EncodeToString(digest[:])
	return s, nil
}

func removalSignal(text string) bool {
	for _, marker := range []string{"SERVICES_", "publish-notification.mjs", "publisher-connection-test.mjs", "services-connection-test.yml", "services_notify:"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// Only individual, statically named result/output bindings are provably
// independent of a changed needs object. Aggregates and indexing fail closed.
var removalNeedsBinding = regexp.MustCompile(`\bneeds\.[A-Za-z_][A-Za-z0-9_-]*\.(?:result|outputs\.[A-Za-z_][A-Za-z0-9_-]*)\b`)

// Check decoded keys and values too: YAML escapes can hide active hooks from
// the raw source scan. Aliases are rejected by workflowYAML.
func decodedRemovalSignal(n *yaml.Node) bool {
	if n.Kind == yaml.ScalarNode && removalSignal(n.Value) {
		return true
	}
	for _, child := range n.Content {
		if decodedRemovalSignal(child) {
			return true
		}
	}
	return false
}

// Remove only structurally validated instrumentation. Source ranges, not YAML
// serialization, preserve all surviving bytes, including pinned build actions.
func removeWorkflowHook(text string) (string, error) {
	if reusableV1Signal(text) {
		return "", errors.New("Remove the reusable v1 Services publication hook manually before retrying removal.")
	}
	if strings.Contains(text, logsCaptureMarker) {
		return originalLogsWorkflow(text)
	}
	fail := func() (string, error) {
		return "", errors.New("Custom or shared Services workflow requires manual review.")
	}
	root, err := workflowYAML(text)
	if err != nil {
		return "", err
	}
	jobs := yamlField(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode || len(jobs.Content) == 0 {
		return fail()
	}
	for i := 1; i < len(jobs.Content); i += 2 {
		job := jobs.Content[i]
		// Reusable workflows can hide active notification hooks outside this
		// snapshot. Absence cannot be established without resolving that graph.
		if job.Kind != yaml.MappingNode || yamlField(job, "uses") != nil {
			return fail()
		}
		steps := yamlField(job, "steps")
		if steps == nil || steps.Kind != yaml.SequenceNode {
			return fail()
		}
	}
	if !removalSignal(text) && !decodedRemovalSignal(root) {
		return text, nil
	}
	if err := validateNotificationHook(text); err != nil {
		return "", err
	}
	lines := strings.SplitAfter(text, "\n")
	removed := map[int]bool{}
	remove := func(n *yaml.Node, indent int) bool {
		start := n.Line - 1
		if n.Style != 0 || start < 0 || start >= len(lines) {
			return false
		}
		line := strings.TrimRight(lines[start], "\r\n")
		if len(line)-len(strings.TrimLeft(line, " ")) != indent {
			return false
		}
		end := start + 1
		for ; end < len(lines); end++ {
			line := strings.TrimRight(lines[end], "\r\n")
			trim := strings.TrimSpace(line)
			if trim != "" && !strings.HasPrefix(trim, "#") && len(line)-len(strings.TrimLeft(line, " ")) <= indent {
				break
			}
		}
		// Trailing comments/blanks belong to the surviving source, not this node.
		for end > start+1 && (strings.TrimSpace(lines[end-1]) == "" || strings.HasPrefix(strings.TrimSpace(lines[end-1]), "#")) {
			end--
		}
		for i := start; i < end; i++ {
			removed[i] = true
		}
		return true
	}
	notifyID, producerID, captureID := "", "", ""
	removedJobs := map[string]bool{}
	removedOutputs := []string{}
	for i := 0; i < len(jobs.Content); i += 2 {
		id, job := jobs.Content[i].Value, jobs.Content[i+1]
		steps := yamlField(job, "steps")
		if steps == nil {
			continue
		}
		for _, step := range steps.Content {
			if strings.TrimSpace(yamlValue(step, "run")) == "node scripts/publish-notification.mjs send" {
				if !exclusiveNotifyJob(job) || !remove(jobs.Content[i], 2) {
					return fail()
				}
				notifyID = id
				removedJobs[id] = true
			}
			if strings.TrimSpace(yamlValue(step, "run")) == "node scripts/publish-notification.mjs capture" {
				producerID, captureID = id, yamlValue(step, "id")
			}
		}
	}
	for i := 0; i < len(jobs.Content); i += 2 {
		id, job := jobs.Content[i].Value, jobs.Content[i+1]
		if id == notifyID {
			continue
		}
		if needsJob(job, notifyID) {
			if !exclusiveFailureJob(job, producerID, notifyID) || !remove(jobs.Content[i], 2) {
				return fail()
			}
			removedJobs[id] = true
			continue
		}
		if id != producerID {
			continue
		}
		steps := yamlField(job, "steps")
		for j, step := range steps.Content {
			if yamlValue(step, "id") == captureID {
				if !onlyYAMLFields(step, "name", "id", "continue-on-error", "env", "run") || !onlyYAMLFields(steps.Content[j+1], "name", "if", "continue-on-error", "uses", "with") || !remove(step, 6) || !remove(steps.Content[j+1], 6) {
					return fail()
				}
			}
			// Retain producer Node setup even when Services originally inserted it:
			// surviving publication commands may now depend on that runtime.
		}
		outputs := yamlField(job, "outputs")
		count := 0
		for j := 0; j < len(outputs.Content); j += 2 {
			if strings.Contains(scalarBinding(outputs.Content[j+1].Value), "steps."+captureID+".outputs.") {
				if !remove(outputs.Content[j], 6) {
					return fail()
				}
				removedOutputs = append(removedOutputs, outputs.Content[j].Value)
				count++
			}
		}
		if count*2 == len(outputs.Content) {
			for j := 0; j < len(job.Content); j += 2 {
				if job.Content[j].Value == "outputs" && !remove(job.Content[j], 4) {
					return fail()
				}
			}
		}
	}
	var out strings.Builder
	for i, line := range lines {
		if !removed[i] {
			out.WriteString(line)
		}
	}
	after := out.String()
	afterRoot, err := workflowYAML(after)
	if err != nil {
		return fail()
	}
	afterJobs := yamlField(afterRoot, "jobs")
	for i := 1; i < len(afterJobs.Content); i += 2 {
		for id := range removedJobs {
			if needsJob(afterJobs.Content[i], id) {
				return fail()
			}
		}
	}
	expressions := expressionBlock.FindAllString(after, -1)
	var conditions func(*yaml.Node)
	conditions = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			expressions = append(expressions, expressionBlock.FindAllString(n.Value, -1)...)
		}
		if n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == "if" && n.Content[i+1].Kind == yaml.ScalarNode {
					expressions = append(expressions, n.Content[i+1].Value)
				}
			}
		}
		for _, child := range n.Content {
			conditions(child)
		}
	}
	conditions(afterRoot)
	// Bracket indexing and wildcard access can hide dependencies from dotted
	// binding checks. Any remaining expression mentioning removed identifiers
	// is conservatively shared, including indirect output consumers.
	for _, expression := range expressions {
		if strings.Contains(expression, "steps") && (strings.Contains(expression, captureID) || strings.Contains(expression, "*")) {
			return fail()
		}
		if strings.Contains(expression, "needs") {
			static := removalNeedsBinding.ReplaceAllStringFunc(expression, func(binding string) string {
				parts := strings.Split(binding, ".")
				if removedJobs[parts[1]] {
					return binding
				}
				if parts[1] == producerID && len(parts) == 4 {
					for _, output := range removedOutputs {
						if parts[3] == output {
							return binding
						}
					}
				}
				return ""
			})
			if strings.Contains(static, "needs") || strings.Contains(expression, "[") {
				return fail()
			}
			if strings.Contains(expression, "*") {
				return fail()
			}
			for id := range removedJobs {
				if strings.Contains(expression, id) {
					return fail()
				}
			}
			for _, output := range removedOutputs {
				if (strings.Contains(expression, producerID) || strings.Contains(expression, "*")) && strings.Contains(expression, output) {
					return fail()
				}
			}
		}
	}
	if removalSignal(after) || decodedRemovalSignal(afterRoot) || strings.Contains(scalarBinding(after), "steps."+captureID+".") || strings.Contains(scalarBinding(after), "needs."+notifyID+".") {
		return fail()
	}
	for _, output := range removedOutputs {
		if strings.Contains(scalarBinding(after), "needs."+producerID+".outputs."+output) {
			return fail()
		}
	}
	return after, nil
}

func onlyYAMLFields(n *yaml.Node, fields ...string) bool {
	if n == nil || n.Kind != yaml.MappingNode || n.Style != 0 {
		return false
	}
	for i := 0; i < len(n.Content); i += 2 {
		found := false
		for _, field := range fields {
			if n.Content[i].Value == field {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func exclusiveNotifyJob(job *yaml.Node) bool {
	if !onlyYAMLFields(job, "name", "needs", "if", "runs-on", "timeout-minutes", "permissions", "steps") {
		return false
	}
	steps := yamlField(job, "steps")
	if steps == nil || len(steps.Content) != 6 {
		return false
	}
	seen := map[string]bool{}
	for _, step := range steps.Content {
		if !onlyYAMLFields(step, "name", "uses", "with", "env", "run") {
			return false
		}
		if yamlValue(step, "uses") != "" {
			uses := strings.SplitN(yamlValue(step, "uses"), "@", 2)[0]
			if seen[uses] || yamlField(step, "run") != nil || yamlField(step, "env") != nil {
				return false
			}
			seen[uses] = true
			if !action(step, "actions/checkout") && !action(step, "actions/setup-node") && !action(step, "actions/download-artifact") && !action(step, "tailscale/github-action") {
				return false
			}
			continue
		}
		run := strings.TrimSpace(yamlValue(step, "run"))
		if run != "node scripts/publish-notification.mjs send" && run != `test -n "$PAYLOAD_ARTIFACT"` && run != legacyPayloadRequirement {
			return false
		}
	}
	return true
}

const legacyPayloadRequirement = `test -n "$PAYLOAD_ARTIFACT" && test -n "$TAILSCALE_CLIENT_ID" && test -n "$TAILSCALE_AUDIENCE" && test -n "$TAILSCALE_TAGS" || {
  echo '::error::Missing captured publish payload or Tailscale federation configuration'
  exit 1
}`

func exclusiveFailureJob(job *yaml.Node, producerID, notifyID string) bool {
	if !onlyYAMLFields(job, "name", "needs", "if", "runs-on", "permissions", "steps") {
		return false
	}
	guard := "always() && github.event_name != 'pull_request' && vars.SERVICES_PUBLISH_NOTIFICATIONS == 'true' && needs." + producerID + ".result == 'success' && needs." + notifyID + ".result == 'failure'"
	if condition(yamlValue(job, "if")) != condition(guard) {
		return false
	}
	needs := yamlField(job, "needs")
	if needs == nil || needs.Kind != yaml.SequenceNode || len(needs.Content) != 2 || !needsJob(job, producerID) {
		return false
	}
	steps := yamlField(job, "steps")
	return steps != nil && len(steps.Content) == 1 && onlyYAMLFields(steps.Content[0], "name", "run") && strings.TrimSpace(yamlValue(steps.Content[0], "run")) == notificationFailureReport
}

const notificationFailureReport = `echo '::error::Image publish succeeded, but Services did not acknowledge its notification. Re-run failed jobs to resend the original retained payload without republishing.'
echo '### Services publish notification failed' >> "$GITHUB_STEP_SUMMARY"
echo 'The image was published successfully. Inspect the notify job; re-run failed jobs to retry its original payload (retained for 30 days). Missing or expired artifacts fail closed and are never reconstructed from current registry state.' >> "$GITHUB_STEP_SUMMARY"
exit 1`
