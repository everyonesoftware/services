package githubapp

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Inspect recognizes the existing Services hook layout; it is intentionally not
// a YAML interpreter, JavaScript security audit, or arbitrary workflow editor.
// Custom layouts that cannot be recognized must be reviewed/adapted manually.
func (c *Client) Inspect(ctx context.Context, app App, repository Repository) (Inspection, error) {
	result := Inspection{Problems: []string{}}
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repository, map[string]string{"metadata": "read", "contents": "read"})
	if err != nil {
		return result, err
	}
	files := map[string]string{}
	missingWorkflow := false
	for _, path := range []string{".github/workflows/verify-and-publish.yml", "scripts/publish-notification.mjs", ".github/workflows/services-connection-test.yml", "scripts/publisher-connection-test.mjs"} {
		var v struct {
			Type     string `json:"type"`
			Encoding string `json:"encoding"`
			Content  string `json:"content"`
		}
		status, err := o.request("GET", "/repos/"+repository.FullName+"/contents/"+path+"?ref="+url.QueryEscape(repository.DefaultBranch), token, nil, &v, 200, 404)
		if err != nil {
			return result, err
		}
		if status == 404 {
			if path == ".github/workflows/verify-and-publish.yml" {
				missingWorkflow = true
			}
			result.Problems = append(result.Problems, "Missing "+path+" on the default branch.")
			continue
		}
		content, err := base64.StdEncoding.DecodeString(v.Content)
		if err != nil || v.Type != "file" || v.Encoding != "base64" || len(strings.TrimSpace(string(content))) == 0 || len(content) > 512<<10 || !utf8.Valid(content) {
			result.Problems = append(result.Problems, "Unsupported file encoding or size for "+path+".")
			continue
		}
		files[path] = string(content)
	}
	// Provisioning can retain a conventional workflow's original filename.
	// Preserve the legacy fast path, but inspect the pinned generated layout
	// when the historical workflow is absent. A plan with pending changes is
	// not installed and must not pass the final apply compatibility check.
	if missingWorkflow {
		snapshot, err := o.planWorkflow(repository, token)
		if err != nil {
			return result, err
		}
		if snapshot.plan.Supported && len(snapshot.plan.Files) == 0 {
			return Inspection{Supported: true, Problems: []string{}}, nil
		}
		if snapshot.plan.Supported {
			return Inspection{Problems: []string{"Open and review the proposed workflow setup pull request. Merge it, then review setup again before applying the publisher connection."}}, nil
		}
		return Inspection{Problems: snapshot.plan.Problems}, nil
	}
	workflow := files[".github/workflows/verify-and-publish.yml"]
	if workflow != "" {
		if err := validateNotificationHook(workflow); err != nil {
			result.Problems = append(result.Problems, "verify-and-publish.yml: "+err.Error())
		}
	}
	sender := withoutComments(files["scripts/publish-notification.mjs"], "//")
	if sender != "" && !containsAll(sender, "export async function runCli", "['capture', 'send'].includes(args[0])", "if (args[0] === 'capture')", "writeFile(path", "readFile(path", "return sendNotification(", "env.SERVICES_PUBLISH_URL", "env.SERVICES_PUBLISH_TOKEN") {
		result.Problems = append(result.Problems, "publish-notification.mjs must implement the existing capture/send CLI using the retained payload and Services environment variables.")
	}
	test := withoutComments(files[".github/workflows/services-connection-test.yml"], "#")
	if test != "" {
		inputs := mappingBlock(mappingBlock(mappingBlock(test, "on", 0), "workflow_dispatch", 2), "inputs", 4)
		validInputs := true
		for _, name := range []string{"connection_id", "challenge"} {
			block := mappingBlock(inputs, name, 6)
			if !containsAll(block, "required: true", "type: string") {
				validInputs = false
			}
		}
		if !validInputs || !containsAll(test, "inputs.connection_id", "inputs.challenge", "secrets.SERVICES_PUBLISH_TOKEN", "vars.SERVICES_PUBLISH_URL", "vars.TAILSCALE_CLIENT_ID", "vars.TAILSCALE_AUDIENCE", "vars.TAILSCALE_TAGS", "tailscale/github-action@") || !runCommand(test, "node scripts/publisher-connection-test.mjs") {
			result.Problems = append(result.Problems, "services-connection-test.yml must declare required string workflow_dispatch inputs connection_id and challenge, join the configured tailnet, and run the connection sender with the Services URL and secret.")
		}
	}
	callback := withoutComments(files["scripts/publisher-connection-test.mjs"], "//")
	if callback != "" && !containsAll(callback, "env.SERVICES_PUBLISH_URL", "env.SERVICES_PUBLISH_TOKEN", "env.SERVICES_CONNECTION_ID", "env.SERVICES_CONNECTION_CHALLENGE", "/api/images/published", "url.pathname = '/api/publishers/callback'", "JSON.stringify({ connectionId, challenge, sourceRepository: repository, ref, runId })") {
		result.Problems = append(result.Problems, "publisher-connection-test.mjs must send the connection challenge and provenance to /api/publishers/callback using the configured Services URL and secret.")
	}
	if len(result.Problems) > 0 {
		result.Problems = append(result.Problems, "Only the checked-in Services hook layout is recognized; custom YAML/JavaScript needs manual review. No workflow files were edited.")
	}
	result.Supported = len(result.Problems) == 0
	return result, nil
}
func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}
func withoutComments(text, prefix string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}
func runCommand(text, command string) bool {
	pattern := `(?m)^\s*run:\s*` + regexp.QuoteMeta(command) + `\s*$`
	return regexp.MustCompile(pattern).MatchString(text)
}

// mappingBlock deliberately accepts only the repository's two-space block
// mapping style (no aliases, flow maps, or duplicate recognized keys).
func mappingBlock(text, key string, indent int) string {
	lines := strings.Split(text, "\n")
	start := -1
	end := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		spaces := len(line) - len(strings.TrimLeft(line, " "))
		if spaces == indent && trimmed == key+":" {
			if start >= 0 {
				return ""
			}
			start = i + 1
			continue
		}
		if start >= 0 && end == len(lines) && spaces <= indent {
			end = i
		}
	}
	if start < 0 {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

// DispatchTest confirms only that GitHub accepted the dispatch. A separately
// authenticated callback is required before a connection can be enabled.
func (c *Client) DispatchTest(ctx context.Context, app App, repository Repository, connectionID, challenge string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`).MatchString(connectionID) || !regexp.MustCompile(`^[A-Za-z0-9_-]{16,512}$`).MatchString(challenge) {
		return errors.New("invalid publisher connection test inputs")
	}
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repository, map[string]string{"metadata": "read", "actions": "write"})
	if err != nil {
		return err
	}
	_, err = o.request("POST", "/repos/"+repository.FullName+"/actions/workflows/services-connection-test.yml/dispatches", token, map[string]any{"ref": repository.DefaultBranch, "inputs": map[string]string{"connection_id": connectionID, "challenge": challenge}}, nil, 204)
	return err
}
