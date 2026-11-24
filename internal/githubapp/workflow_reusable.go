package githubapp

import (
	"errors"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	reusableCapture = "everyonesoftware/workflows-common/actions/capture-image-v1"
	reusableNotify  = "everyonesoftware/workflows-common/.github/workflows/notify-image-v1.yml"
)

// Detect incomplete or lookalike integrations too, so a bad pin or binding
// cannot fall through to conventional instrumentation and add a second hook.
func reusableV1Signal(text string) bool {
	root, err := workflowYAML(text)
	if err != nil {
		return reusableV1Marker(text)
	}
	return countReusableV1Uses(root) != 0
}

func reusableV1Marker(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "capture-image-v1") || strings.Contains(s, "notify-image-v1")
}

func countReusableV1Uses(n *yaml.Node) int {
	count := 0
	if reusableV1Marker(yamlValue(n, "uses")) {
		count++
	}
	for _, child := range n.Content {
		count += countReusableV1Uses(child)
	}
	return count
}

func reusableV1HasLocalSender(n *yaml.Node) bool {
	if n.Kind == yaml.ScalarNode && strings.Contains(n.Value, "publish-notification.mjs") {
		return true
	}
	for _, child := range n.Content {
		if reusableV1HasLocalSender(child) {
			return true
		}
	}
	return false
}

func pinnedReusableV1(n *yaml.Node, path string) bool {
	uses, sha, ok := strings.Cut(yamlValue(n, "uses"), "@")
	return ok && uses == path && gitSHA.MatchString(sha)
}

func reusableV1Bindings(n *yaml.Node, expected map[string]string) bool {
	if n == nil || n.Kind != yaml.MappingNode || len(n.Content) != 2*len(expected) {
		return false
	}
	for k, v := range expected {
		if !sameBinding(yamlValue(n, k), v) {
			return false
		}
	}
	return true
}

var (
	reusableJobID   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
	reusableDigest  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	reusableVersion = regexp.MustCompile(`^[1-9][0-9]*$`)
	reusableTag     = regexp.MustCompile(`^ghcr\.io/([a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*/)+[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

func completeExpression(s string) bool {
	return s != "" && expressionBlock.FindString(s) == s && strings.TrimSpace(s[3:len(s)-2]) != ""
}

func reusableV1CaptureInputs(n *yaml.Node) bool {
	if n == nil || n.Kind != yaml.MappingNode || len(n.Content) != 6 {
		return false
	}
	digest, version, tags := yamlValue(n, "digest"), yamlValue(n, "version"), strings.TrimSpace(yamlValue(n, "tags"))
	if (!reusableDigest.MatchString(digest) && !completeExpression(digest)) ||
		(!reusableVersion.MatchString(version) && !completeExpression(version)) || tags == "" {
		return false
	}
	for _, tag := range strings.Split(tags, "\n") {
		tag = strings.TrimSpace(tag)
		if !completeExpression(tag) && !reusableTag.MatchString(expressionBlock.ReplaceAllString(tag, "expr")) {
			return false
		}
	}
	return true
}

// Recognize the declared interface, not the publisher's build implementation.
// Exact repository paths and full pins identify the trusted, immutable code;
// this does not audit the pinned code or prove delivery. Runtime payload checks
// and receiver authorization for the connection's exact image still apply.
func validateReusableV1(text, defaultBranch string) error {
	fail := func(reason string) error {
		return errors.New(reason + " Review the reusable v1 Services publication hook manually.")
	}
	root, err := workflowYAML(text)
	if err != nil {
		return err
	}
	if err := validatePublicationEvents(root); err != nil {
		return err
	}
	if reusableV1HasLocalSender(root) || strings.Contains(text, logsCaptureMarker) {
		return fail("Legacy and reusable Services hooks cannot coexist.")
	}
	jobs := yamlField(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return fail("Expected a producer and a reusable notification job.")
	}
	var producer, notify *yaml.Node
	var producerID string
	captures, callers := 0, 0
	for i := 0; i < len(jobs.Content); i += 2 {
		id, job := jobs.Content[i].Value, jobs.Content[i+1]
		if strings.Contains(strings.ToLower(yamlValue(job, "uses")), "notify-image-v1") {
			callers++
			notify = job
		}
		steps := yamlField(job, "steps")
		if steps == nil || steps.Kind != yaml.SequenceNode {
			continue
		}
		ids := 0
		for _, step := range steps.Content {
			if strings.EqualFold(yamlValue(step, "id"), "services_capture") {
				ids++
			}
			if !strings.Contains(strings.ToLower(yamlValue(step, "uses")), "capture-image-v1") {
				continue
			}
			if !pinnedReusableV1(step, reusableCapture) || yamlValue(step, "id") != "services_capture" ||
				yamlField(step, "run") != nil || !reusableV1CaptureInputs(yamlField(step, "with")) {
				return fail("Expected the pinned services_capture action with digest, tags and version inputs.")
			}
			captures++
			producer, producerID = job, id
		}
		if producer == job && ids != 1 {
			return fail("The services_capture step identifier must be unique within its producer.")
		}
	}
	if captures != 1 || callers != 1 || countReusableV1Uses(root) != 2 || producer == notify || !reusableJobID.MatchString(producerID) ||
		!pinnedReusableV1(notify, reusableNotify) {
		return fail("Expected exactly one pinned capture action and one pinned notification caller.")
	}
	if yamlField(producer, "uses") != nil || yamlField(producer, "strategy") != nil || yamlField(producer, "continue-on-error") != nil ||
		!sameBinding(yamlValue(yamlField(producer, "outputs"), "services_artifact"), "${{ steps.services_capture.outputs.artifact }}") {
		return fail("The producer must expose the capture action's artifact output and fail on job error.")
	}
	// A workflow call cannot contain runner steps or execution overrides. Keep
	// this list small so the inspected trust boundary matches the declaration.
	for i := 0; i < len(notify.Content); i += 2 {
		switch notify.Content[i].Value {
		case "name", "uses", "needs", "if", "permissions", "with", "secrets":
		default:
			return fail("The notification caller contains unsupported fields.")
		}
	}
	needs := yamlField(notify, "needs")
	if needs != nil && needs.Kind == yaml.SequenceNode && len(needs.Content) == 1 {
		needs = needs.Content[0]
	}
	if needs == nil || needs.Kind != yaml.ScalarNode || needs.Value != producerID || defaultBranch == "" {
		return fail("The notification caller must depend only on the artifact producer.")
	}
	artifact := "needs." + producerID + ".outputs.services_artifact"
	guard := "!cancelled() && (github.event_name == 'push' || github.event_name == 'workflow_dispatch') && github.ref == 'refs/heads/" +
		strings.ReplaceAll(defaultBranch, "'", "''") + "' && needs." + producerID + ".result == 'success' && " + artifact +
		" != '' && vars.SERVICES_PUBLISH_NOTIFICATIONS == 'true'"
	if condition(yamlValue(notify, "if")) != condition(guard) {
		return fail("The notification caller must gate on push or dispatch to the default branch, producer success, artifact presence and enabled notifications.")
	}
	if !reusableV1Bindings(yamlField(notify, "permissions"), map[string]string{"contents": "read", "id-token": "write"}) ||
		!reusableV1Bindings(yamlField(notify, "with"), map[string]string{
			"artifact": "${{ " + artifact + " }}", "services-url": "${{ vars.SERVICES_PUBLISH_URL }}",
			"tailscale-client-id": "${{ vars.TAILSCALE_CLIENT_ID }}", "tailscale-audience": "${{ vars.TAILSCALE_AUDIENCE }}",
			"tailscale-tags": "${{ vars.TAILSCALE_TAGS }}",
		}) || !reusableV1Bindings(yamlField(notify, "secrets"), map[string]string{"services-token": "${{ secrets.SERVICES_PUBLISH_TOKEN }}"}) {
		return fail("The notification caller must pass exactly the v1 artifact, variables, OIDC permissions and Services token.")
	}
	return nil
}
