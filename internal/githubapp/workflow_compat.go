package githubapp

import (
	"regexp"

	"gopkg.in/yaml.v3"
)

// Bash defaults do not change where the generated repository scripts run.
// Working directories, expression shells, and custom shell templates do.
func supportedWorkflowDefaults(root *yaml.Node) bool {
	defaults := yamlField(root, "defaults")
	if defaults == nil {
		return true
	}
	if defaults.Kind != yaml.MappingNode || len(defaults.Content) != 2 || defaults.Content[0].Value != "run" {
		return false
	}
	run := defaults.Content[1]
	return run.Kind == yaml.MappingNode && len(run.Content) == 2 && run.Content[0].Value == "shell" && yamlValue(run, "shell") == "bash"
}

// Preserve an existing repository-wide static serialization group instead of
// adding a second concurrency key or changing cancellation/queue behavior.
// Dynamic groups can partition publishers by run/ref, so need manual review.
func supportedExistingConcurrency(root *yaml.Node) bool {
	concurrency := yamlField(root, "concurrency")
	if concurrency == nil {
		return true
	}
	if concurrency.Kind != yaml.MappingNode {
		return false
	}
	group := yamlField(concurrency, "group")
	if group == nil || group.Kind != yaml.ScalarNode || group.Tag != "!!str" || !regexp.MustCompile(`^[A-Za-z0-9_./:-]+$`).MatchString(group.Value) {
		return false
	}
	for i := 0; i < len(concurrency.Content); i += 2 {
		value := concurrency.Content[i+1]
		switch concurrency.Content[i].Value {
		case "group":
		case "cancel-in-progress":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!bool" || value.Value != "false" {
				return false
			}
		case "queue":
			if value.Kind != yaml.ScalarNode || (value.Value != "single" && value.Value != "max") {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// A branch-guarded version job is safe when the consuming job uses GitHub's
// default success dependency gate: a skipped/failed source also skips publishing.
// Do not accept arbitrary conditions, ignored failures, or status overrides.
func availableVersionJob(source, consumer *yaml.Node) bool {
	if unconditional(source) {
		return true
	}
	return yamlField(source, "continue-on-error") == nil && unconditional(consumer) &&
		regexp.MustCompile(`^github\.ref=='refs/heads/[A-Za-z0-9_./-]+'$`).MatchString(condition(yamlValue(source, "if")))
}

// Bind to one earlier, unconditional producer, not to guessed or re-evaluated
// tags. Shell-produced tags are validated at capture/send time by the sender's
// single-GHCR-image checks, then by the receiver's exact image allowlist. Never
// execute repository shell code in Services or try to infer its output.
func publicationTagProducer(steps *yaml.Node, before int, tags string) *yaml.Node {
	match := regexp.MustCompile(`^\$\{\{steps\.([A-Za-z_][A-Za-z0-9_]*)\.outputs\.tags\}\}$`).FindStringSubmatch(scalarBinding(tags))
	if match == nil || steps == nil || before > len(steps.Content) {
		return nil
	}
	for _, step := range steps.Content[:before] {
		if yamlValue(step, "id") != match[1] || !unconditional(step) {
			continue
		}
		if action(step, "docker/metadata-action") {
			return step
		}
		if yamlValue(step, "run") != "" && yamlField(step, "uses") == nil && yamlField(step, "working-directory") == nil &&
			(yamlField(step, "shell") == nil || yamlValue(step, "shell") == "bash") {
			return step
		}
	}
	return nil
}
