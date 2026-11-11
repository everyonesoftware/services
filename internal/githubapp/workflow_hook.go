package githubapp

import (
	"errors"
	"regexp"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const publicationEventGate = "(github.event_name == 'push' || github.event_name == 'workflow_dispatch' || github.event_name == 'release')"

// Only these publication triggers are understood. PR verification may coexist,
// but privileged PR, workflow_run and custom dispatch chains need manual review.
func validatePublicationEvents(root *yaml.Node) error {
	on := yamlField(root, "on")
	events := []string{}
	if on != nil {
		switch on.Kind {
		case yaml.ScalarNode:
			events = append(events, on.Value)
		case yaml.SequenceNode:
			for _, event := range on.Content {
				if event.Kind != yaml.ScalarNode {
					return errors.New("Complex workflow events require manual review.")
				}
				events = append(events, event.Value)
			}
		case yaml.MappingNode:
			for i := 0; i < len(on.Content); i += 2 {
				events = append(events, on.Content[i].Value)
			}
		}
	}
	publication := false
	for _, event := range events {
		switch event {
		case "push", "workflow_dispatch", "release":
			publication = true
		case "pull_request":
		default:
			return errors.New("Unsupported or unsafe publication event: " + event + ". Review the workflow manually.")
		}
	}
	if !publication {
		return errors.New("A known publication event (push, workflow_dispatch or release) is required.")
	}
	return nil
}

// Strip whitespace only in expression syntax, never inside quoted literals or
// ordinary scalar text (including paths, shell commands and literal image tags).
func expressionSyntax(s string) string {
	var b strings.Builder
	quoted := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\'' {
			b.WriteByte(c)
			if quoted && i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte(s[i+1])
				i++
				continue
			}
			quoted = !quoted
		} else if quoted || !unicode.IsSpace(rune(c)) {
			b.WriteByte(c)
		} else {
			// Whitespace cannot concatenate two identifier/number tokens.
			word := func(c byte) bool {
				return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
			}
			end := i + 1
			for end < len(s) && unicode.IsSpace(rune(s[end])) {
				end++
			}
			if i > 0 && end < len(s) && word(s[i-1]) && word(s[end]) {
				b.WriteByte(' ')
			}
			i = end - 1
		}
	}
	return b.String()
}

var expressionBlock = regexp.MustCompile(`\$\{\{[\s\S]*?\}\}`)

func scalarBinding(s string) string {
	return expressionBlock.ReplaceAllStringFunc(s, expressionSyntax)
}
func condition(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "${{") && strings.HasSuffix(s, "}}") {
		s = s[3 : len(s)-2]
	}
	return expressionSyntax(s)
}
func sameBinding(a, b string) bool { return a != "" && scalarBinding(a) == scalarBinding(b) }
func needsJob(job *yaml.Node, id string) bool {
	n := yamlField(job, "needs")
	if n == nil {
		return false
	}
	if n.Kind == yaml.ScalarNode {
		return n.Value == id
	}
	if n.Kind == yaml.SequenceNode {
		for _, v := range n.Content {
			if v.Kind == yaml.ScalarNode && v.Value == id {
				return true
			}
		}
	}
	return false
}
func hostedPublicationRunner(job *yaml.Node) bool {
	switch yamlValue(job, "runs-on") {
	case "ubuntu-latest", "ubuntu-22.04", "ubuntu-24.04":
		return true
	default:
		return false
	}
}

func unconditional(step *yaml.Node) bool {
	return yamlField(step, "if") == nil && yamlField(step, "continue-on-error") == nil
}
func tagsBinding(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' })
	for i := range parts {
		parts[i] = scalarBinding(strings.TrimSpace(parts[i]))
	}
	return strings.Join(parts, "\n")
}

func originalCheckout(steps *yaml.Node, before int) bool {
	checkedOut := false
	for _, step := range steps.Content[:before] {
		if !action(step, "actions/checkout") {
			continue
		}
		with := yamlField(step, "with")
		ref := yamlValue(with, "ref")
		if !unconditional(step) || yamlValue(with, "path") != "" || yamlValue(with, "repository") != "" || (ref != "" && !sameBinding(ref, "${{ github.sha }}")) {
			return false
		}
		checkedOut = true
	}
	return checkedOut
}

// Resolve only known scalar version sources. Evaluating arbitrary expressions,
// external outputs or shell code is outside the planner's safety contract.
func versionSource(root, job *yaml.Node, steps *yaml.Node, before int, value string, depth int) bool {
	if depth > 8 {
		return false
	}
	value = scalarBinding(value)
	if regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(value) || value == "${{github.run_number}}" {
		return true
	}
	if m := regexp.MustCompile(`^\$\{\{steps\.([A-Za-z_][A-Za-z0-9_]*)\.outputs\.[A-Za-z_][A-Za-z0-9_]*\}\}$`).FindStringSubmatch(value); m != nil {
		if steps == nil || before > len(steps.Content) {
			return false
		}
		for _, step := range steps.Content[:before] {
			if yamlValue(step, "id") == m[1] && unconditional(step) && (yamlValue(step, "run") != "" || yamlValue(step, "uses") != "") {
				return true
			}
		}
	}
	if m := regexp.MustCompile(`^\$\{\{needs\.([A-Za-z_][A-Za-z0-9_-]*)\.outputs\.([A-Za-z_][A-Za-z0-9_]*)\}\}$`).FindStringSubmatch(value); m != nil {
		source := yamlField(yamlField(root, "jobs"), m[1])
		sourceSteps := yamlField(source, "steps")
		if source == nil || sourceSteps == nil || !needsJob(job, m[1]) || !unconditional(source) {
			return false
		}
		return versionSource(root, source, sourceSteps, len(sourceSteps.Content), yamlValue(yamlField(source, "outputs"), m[2]), depth+1)
	}
	if m := regexp.MustCompile(`^\$\{\{env\.([A-Za-z_][A-Za-z0-9_]*)\}\}$`).FindStringSubmatch(value); m != nil {
		v := yamlField(yamlField(job, "env"), m[1])
		if v == nil {
			v = yamlField(yamlField(root, "env"), m[1])
		}
		return v != nil && versionSource(root, job, steps, before, v.Value, depth+1)
	}
	return false
}

// Recognize only explicit version arguments, preserving their original source.
// Multiple recognized names are safe only when they bind to the same value;
// choosing by precedence could silently announce a different application version.
func publicationBuildVersion(with *yaml.Node) (string, error) {
	args := yamlValue(with, "build-args")
	// Support only plain newline-delimited arguments. The action interprets CSV
	// quoting, so even an unrecognized argument could hide a version binding.
	// Reject the entire unsupported input rather than guessing or rewriting it.
	if strings.ContainsAny(args, "\",") {
		return "", errors.New("Quoted or CSV build arguments are unsupported; use plain newline-delimited arguments or review the hook manually.")
	}
	version := ""
	seen := map[string]bool{}
	for _, line := range strings.Split(args, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if name != "APP_VERSION" && name != "VERSION" && name != "BUILD_VERSION" {
			continue
		}
		if seen[name] {
			return "", errors.New("Duplicate " + name + " build arguments are ambiguous; keep one explicit binding.")
		}
		seen[name] = true
		if !ok || value == "" {
			return "", errors.New("Set an explicit numeric version source for the " + name + " build argument.")
		}
		if version != "" && !sameBinding(version, value) {
			return "", errors.New("Conflicting APP_VERSION, VERSION or BUILD_VERSION build arguments are ambiguous; bind recognized version arguments to the same source.")
		}
		version = value
	}
	if version == "" {
		return "", errors.New("Automatic Services hook setup needs an explicit APP_VERSION, VERSION or BUILD_VERSION build argument bound to the existing numeric version source; otherwise review the hook manually.")
	}
	return version, nil
}

// Recognize a closed, structurally linked capture -> artifact -> send chain.
// Arbitrary YAML/expressions are deliberately unsupported, not approximated by
// substring checks that might approve a different producer or retained file.
func validateNotificationHook(text string) error {
	fail := func(reason string) error {
		return errors.New(reason + " Review the Services publication hook manually.")
	}
	root, err := workflowYAML(text)
	if err != nil {
		return err
	}
	if err = validatePublicationEvents(root); err != nil {
		return err
	}
	jobs := yamlField(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode || buildPushCount(root) != 1 {
		return fail("Expected one actual Docker push producer.")
	}
	var producer, steps, push, capture, upload, notify *yaml.Node
	producerID := ""
	pushIndex := -1
	captures, sends := 0, 0
	for i := 0; i < len(jobs.Content); i += 2 {
		job := jobs.Content[i+1]
		ss := yamlField(job, "steps")
		if ss == nil {
			continue
		}
		ids := map[string]bool{}
		for j, step := range ss.Content {
			id := yamlValue(step, "id")
			if id != "" {
				if ids[id] {
					return fail("Step identifiers must be unique.")
				}
				ids[id] = true
			}
			if action(step, "docker/build-push-action") {
				producer = job
				producerID = jobs.Content[i].Value
				steps = ss
				push = step
				pushIndex = j
			}
			switch strings.TrimSpace(yamlValue(step, "run")) {
			case "node scripts/publish-notification.mjs capture":
				captures++
			case "node scripts/publish-notification.mjs send":
				sends++
				notify = job
			}
		}
	}
	if captures != 1 || sends != 1 || pushIndex < 0 || pushIndex+2 >= len(steps.Content) {
		return fail("Expected one adjacent capture and artifact upload after the push, and one sender.")
	}
	for _, job := range []*yaml.Node{producer, notify} {
		for _, key := range []string{"strategy", "container", "services", "defaults", "uses", "continue-on-error"} {
			if yamlField(job, key) != nil {
				return fail("Custom publication execution is unsupported.")
			}
		}
		if !hostedPublicationRunner(job) {
			return fail("Only hosted ubuntu-latest, ubuntu-22.04 or ubuntu-24.04 publication execution is supported.")
		}
	}
	if yamlField(root, "defaults") != nil || !unconditional(push) || yamlValue(yamlField(push, "with"), "push") != "true" || yamlField(push, "env") != nil {
		return fail("The actual push must be unconditional, push:true and fail on error.")
	}
	if context := yamlValue(yamlField(push, "with"), "context"); context != "" && context != "." {
		return fail("A custom build context cannot prove original publication provenance.")
	}
	if !originalCheckout(steps, pushIndex) {
		return fail("Publication must check out the original repository and commit before pushing.")
	}
	pushID := yamlValue(push, "id")
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(pushID) {
		return fail("The actual push needs a unique explicit id.")
	}
	capture = steps.Content[pushIndex+1]
	upload = steps.Content[pushIndex+2]
	captureID := yamlValue(capture, "id")
	if strings.TrimSpace(yamlValue(capture, "run")) != "node scripts/publish-notification.mjs capture" || captureID == "" || yamlField(capture, "if") != nil {
		return fail("Capture must immediately follow the actual push.")
	}
	for _, step := range []*yaml.Node{push, capture, upload} {
		if yamlField(step, "working-directory") != nil || yamlField(step, "shell") != nil {
			return fail("Custom publication working directories or shells are unsupported.")
		}
	}
	env := yamlField(capture, "env")
	with := yamlField(push, "with")
	if !sameBinding(yamlValue(env, "PUBLISHED_DIGEST"), "${{ steps."+pushID+".outputs.digest }}") {
		return fail("Captured digest must bind to the actual push output.")
	}
	tags := yamlValue(with, "tags")
	if tags == "" || tagsBinding(tags) != tagsBinding(yamlValue(env, "PUBLISHED_TAGS")) {
		return fail("Captured tags must equal the actual push inputs.")
	}
	match := regexp.MustCompile(`^\$\{\{steps\.([A-Za-z_][A-Za-z0-9_]*)\.outputs\.tags\}\}$`).FindStringSubmatch(scalarBinding(tags))
	if match != nil {
		found := false
		for _, step := range steps.Content[:pushIndex] {
			if yamlValue(step, "id") == match[1] && action(step, "docker/metadata-action") && unconditional(step) {
				found = true
			}
		}
		if !found {
			return fail("The actual tag metadata producer is missing or conditional.")
		}
	}
	version, err := publicationBuildVersion(with)
	if err != nil {
		return fail(err.Error())
	}
	if !sameBinding(yamlValue(env, "APP_VERSION"), version) {
		return fail("Captured APP_VERSION must equal the actual build argument source.")
	}
	if !versionSource(root, producer, steps, pushIndex, version, 0) {
		return fail("APP_VERSION must resolve to an existing unconditional version source.")
	}
	payload := "${{ runner.temp }}/services-publish/published.json"
	artifact := "${{ steps." + captureID + ".outputs.artifact }}"
	if !sameBinding(yamlValue(env, "SERVICES_PUBLISH_PAYLOAD_PATH"), payload) || !action(upload, "actions/upload-artifact") || condition(yamlValue(upload, "if")) != condition("steps."+captureID+".outcome == 'success'") || !sameBinding(yamlValue(yamlField(upload, "with"), "name"), artifact) || !sameBinding(yamlValue(yamlField(upload, "with"), "path"), payload) {
		return fail("Upload must retain the captured file under the capture output name after actual capture success.")
	}
	uploadWith := yamlField(upload, "with")
	if yamlValue(uploadWith, "if-no-files-found") != "error" || yamlValue(uploadWith, "overwrite") != "false" {
		return fail("Artifact retention must fail closed and never overwrite.")
	}
	output := ""
	outputs := yamlField(producer, "outputs")
	if outputs == nil || outputs.Kind != yaml.MappingNode {
		return fail("Producer job outputs must be a mapping bound to capture.")
	}
	if outputs != nil {
		for i := 0; i < len(outputs.Content); i += 2 {
			if sameBinding(outputs.Content[i+1].Value, artifact) {
				if output != "" {
					return fail("Artifact job output is ambiguous.")
				}
				output = outputs.Content[i].Value
			}
		}
	}
	if output == "" || !needsJob(notify, producerID) {
		return fail("Notify must depend on the producer and its captured artifact output.")
	}
	success := "needs." + producerID + ".result == 'success' && vars.SERVICES_PUBLISH_NOTIFICATIONS == 'true'"
	guard := condition(yamlValue(notify, "if"))
	// Legacy checked-in layout is safe only after validating the closed trigger
	// set above: excluding PR then leaves exactly the known publication events.
	if guard != condition(publicationEventGate+" && "+success) && guard != condition("github.event_name != 'pull_request' && "+success) {
		return fail("Notify requires actual producer success, explicit opt-in and a safe publication event gate.")
	}
	downloadIndex, sendIndex, federationIndex := -1, -1, -1
	notifySteps := yamlField(notify, "steps")
	for i, step := range notifySteps.Content {
		if action(step, "actions/download-artifact") {
			if downloadIndex >= 0 || !unconditional(step) {
				return fail("Expected one unconditional retained artifact download.")
			}
			downloadIndex = i
			w := yamlField(step, "with")
			if w == nil || w.Kind != yaml.MappingNode {
				return fail("Download inputs must be a mapping.")
			}
			for j := 0; j < len(w.Content); j += 2 {
				switch w.Content[j].Value {
				case "name", "path", "digest-mismatch":
				default:
					return fail("Download inputs must not override retained artifact provenance.")
				}
			}
			if !sameBinding(yamlValue(w, "name"), "${{ needs."+producerID+".outputs."+output+" }}") || !sameBinding(yamlValue(w, "path"), "${{ runner.temp }}/services-publish") || yamlValue(w, "digest-mismatch") != "error" {
				return fail("Download must bind to the producer artifact output and retained payload directory.")
			}
		}
		if strings.TrimSpace(yamlValue(step, "run")) == "node scripts/publish-notification.mjs send" {
			sendIndex = i
			e := yamlField(step, "env")
			if !unconditional(step) || yamlField(step, "working-directory") != nil || yamlField(step, "shell") != nil || !sameBinding(yamlValue(e, "SERVICES_PUBLISH_PAYLOAD_PATH"), payload) || !sameBinding(yamlValue(e, "SERVICES_PUBLISH_URL"), "${{ vars.SERVICES_PUBLISH_URL }}") || !sameBinding(yamlValue(e, "SERVICES_PUBLISH_TOKEN"), "${{ secrets.SERVICES_PUBLISH_TOKEN }}") {
				return fail("Send must use the retained file and configured Services credentials.")
			}
		}
		if action(step, "tailscale/github-action") {
			if federationIndex >= 0 || !unconditional(step) {
				return fail("One unconditional Tailscale federation step is required.")
			}
			federationIndex = i
			w := yamlField(step, "with")
			for key, variable := range map[string]string{"oauth-client-id": "TAILSCALE_CLIENT_ID", "audience": "TAILSCALE_AUDIENCE", "tags": "TAILSCALE_TAGS"} {
				if !sameBinding(yamlValue(w, key), "${{ vars."+variable+" }}") {
					return fail("Tailscale federation must use the configured variables.")
				}
			}
		}
	}
	if downloadIndex < 0 || federationIndex != downloadIndex+1 || sendIndex != federationIndex+1 {
		return fail("Only federation may intervene between the retained artifact download and sender.")
	}
	if !originalCheckout(notifySteps, downloadIndex) {
		return fail("Notify must check out the original repository and commit before downloading the payload.")
	}
	return nil
}
