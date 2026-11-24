package githubapp

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// WorkflowFile is an exact, reviewable file change. No repository secrets or
// variable values are fetched while constructing a plan. Empty After denotes
// file deletion (empty repository files are not supported by this planner).
type WorkflowFile struct {
	Path   string `json:"path"`
	Before string `json:"before"`
	After  string `json:"after"`
}
type WorkflowPlan struct {
	Supported   bool           `json:"supported"`
	Problems    []string       `json:"problems"`
	Files       []WorkflowFile `json:"files"`
	Fingerprint string         `json:"fingerprint"`
}

//go:embed templates/publish-notification.mjs
var senderTemplate string

//go:embed templates/publisher-connection-test.mjs
var callbackTemplate string

//go:embed templates/services-connection-test.yml
var connectionTemplate string

var gitSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)

const senderPath = "scripts/publish-notification.mjs"
const callbackPath = "scripts/publisher-connection-test.mjs"
const connectionPath = ".github/workflows/services-connection-test.yml"

type workflowSnapshot struct {
	plan      WorkflowPlan
	sha, tree string
}
type gitReference struct {
	Ref    string `json:"ref"`
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

func (c *Client) PlanWorkflow(ctx context.Context, app App, repo Repository) (WorkflowPlan, error) {
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repo, map[string]string{"metadata": "read", "contents": "read"})
	if err != nil {
		return WorkflowPlan{}, err
	}
	snapshot, err := o.planWorkflow(repo, token)
	return snapshot.plan, err
}

func (o *operation) workflowRef(repo Repository, token, branch string) (gitReference, int, error) {
	var ref gitReference
	status, err := o.request("GET", "/repos/"+repo.FullName+"/git/ref/heads/"+url.PathEscape(branch), token, nil, &ref, 200, 404)
	if err == nil && status == 200 && (ref.Ref != "refs/heads/"+branch || ref.Object.Type != "commit" || !gitSHA.MatchString(ref.Object.SHA)) {
		err = errors.New("GitHub returned invalid branch metadata")
	}
	return ref, status, err
}
func (o *operation) workflowContent(repo Repository, token, path, sha string) (string, bool, error) {
	var v struct{ Type, Encoding, Content string }
	status, err := o.request("GET", "/repos/"+repo.FullName+"/contents/"+path+"?ref="+url.QueryEscape(sha), token, nil, &v, 200, 404)
	if err != nil || status == 404 {
		return "", false, err
	}
	data, err := base64.StdEncoding.DecodeString(v.Content)
	if err != nil || v.Type != "file" || v.Encoding != "base64" || len(data) > 512<<10 || !utf8.Valid(data) || len(data) == 0 {
		return "", false, errors.New("GitHub workflow file encoding or size is unsupported")
	}
	return string(data), true, nil
}
func existingHook(s string) bool {
	return validateNotificationHook(s) == nil
}
func supportedSender(s string) bool {
	return containsAll(withoutComments(s, "//"), "export async function runCli", "['capture', 'send'].includes(args[0])", "if (args[0] === 'capture')", "writeFile(path", "readFile(path", "return sendNotification(", "env.SERVICES_PUBLISH_URL", "env.SERVICES_PUBLISH_TOKEN")
}
func (o *operation) planWorkflow(repo Repository, token string) (workflowSnapshot, error) {
	return o.planWorkflowKind(repo, token, false)
}

func (o *operation) planWorkflowKind(repo Repository, token string, removal bool) (workflowSnapshot, error) {
	s := workflowSnapshot{plan: WorkflowPlan{Problems: []string{}, Files: []WorkflowFile{}}}
	ref, status, err := o.workflowRef(repo, token, repo.DefaultBranch)
	if err != nil {
		return s, err
	}
	if status != 200 {
		return s, errors.New("GitHub default branch is missing")
	}
	s.sha = ref.Object.SHA
	var commit struct {
		SHA  string `json:"sha"`
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	base := "/repos/" + repo.FullName
	if _, err = o.request("GET", base+"/git/commits/"+s.sha, token, nil, &commit, 200); err != nil {
		return s, err
	}
	if commit.SHA != s.sha || !gitSHA.MatchString(commit.Tree.SHA) {
		return s, errors.New("GitHub returned invalid base commit")
	}
	s.tree = commit.Tree.SHA
	var entries []struct{ Type, Path string }
	status, err = o.request("GET", base+"/contents/.github/workflows?ref="+s.sha, token, nil, &entries, 200, 404)
	if err != nil {
		return s, err
	}
	if o.next || (status == 200 && entries == nil) {
		return s, errors.New("GitHub workflow directory listing is incomplete")
	}
	if len(entries) > 80 {
		return s, errors.New("GitHub workflow count exceeds supported limit")
	}
	paths := []string{}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Type != "file" || !regexp.MustCompile(`^\.github/workflows/[A-Za-z0-9_.-]+\.ya?ml$`).MatchString(e.Path) {
			return s, errors.New("GitHub workflow directory contains unsupported entries")
		}
		if seen[e.Path] {
			return s, errors.New("GitHub workflow directory contains duplicates")
		}
		seen[e.Path] = true
		paths = append(paths, e.Path)
	}
	sort.Strings(paths)
	files := map[string]string{}
	readFiles := func(paths []string) error {
		for _, path := range paths {
			content, _, err := o.workflowContent(repo, token, path, s.sha)
			if err != nil {
				return err
			}
			files[path] = content
		}
		return nil
	}
	if err := readFiles(paths); err != nil {
		return s, err
	}
	problem := func(message string) (workflowSnapshot, error) {
		s.plan.Problems = append(s.plan.Problems, message)
		s.plan.Files = []WorkflowFile{}
		return s, nil
	}
	if removal {
		if err := readFiles([]string{senderPath, callbackPath}); err != nil {
			return s, err
		}
		return planRemovalSnapshot(repo, s, paths, files)
	}
	hooks := 0
	logsHook, reusableHook := false, false
	for _, path := range paths {
		if reusableV1Signal(files[path]) {
			if err := validateReusableV1(files[path], repo.DefaultBranch); err != nil {
				return problem(path + ": " + err.Error())
			}
			hooks++
			reusableHook = true
		} else if existingHook(files[path]) {
			hooks++
			logsHook = logsHook || strings.Contains(files[path], logsCaptureMarker)
		} else if strings.Contains(files[path], "publish-notification.mjs") {
			err := validateNotificationHook(files[path])
			return problem("An existing Services hook is incomplete or custom: " + err.Error())
		}
	}
	if hooks > 1 {
		return problem("Multiple Services publication hooks are ambiguous; review them manually.")
	}
	localPaths := []string{callbackPath}
	if !reusableHook {
		localPaths = append(localPaths, senderPath)
	}
	if err := readFiles(localPaths); err != nil {
		return s, err
	}
	if hooks == 0 {
		candidate := ""
		logsCandidate := false
		count := 0
		for _, path := range paths {
			root, e := workflowYAML(files[path])
			if e != nil {
				return problem(e.Error())
			}
			logsPushes := logsRegistryCount(root)
			n := buildPushCount(root) + logsPushes
			count += n
			if n > 0 {
				candidate = path
				logsCandidate = logsPushes != 0
			}
		}
		if count != 1 {
			return problem("Expected exactly one Docker publication workflow; custom or ambiguous layouts require manual review.")
		}
		provision := provisionConventionalWorkflow
		if logsCandidate {
			provision = provisionLogsWorkflow
		}
		after, e := provision(files[candidate])
		if e != nil {
			return problem(e.Error())
		}
		if logsCandidate {
			if e := validateNotificationHook(after); e != nil {
				return problem(e.Error())
			}
			logsHook = true
		}
		s.plan.Files = append(s.plan.Files, WorkflowFile{Path: candidate, Before: files[candidate], After: after})
		if files[senderPath] == "" {
			s.plan.Files = append(s.plan.Files, WorkflowFile{Path: senderPath, After: senderTemplate})
		} else if !supportedSender(files[senderPath]) {
			return problem("The existing publication sender is custom; it will not be overwritten.")
		}
	} else if !reusableHook && !supportedSender(files[senderPath]) {
		return problem("The existing publication sender is missing or unsupported; review the existing hook manually.")
	}
	if logsHook {
		before, _, e := o.workflowContent(repo, token, logsPublisherPath, s.sha)
		if e != nil {
			return s, e
		}
		files[logsPublisherPath] = before
		after, e := provisionLogsPublisher(before)
		if e != nil {
			return problem(e.Error())
		}
		if after != before {
			if hooks != 0 {
				return problem("The installed Logs hook is missing verified registry outputs; review it manually.")
			}
			s.plan.Files = append(s.plan.Files, WorkflowFile{Path: logsPublisherPath, Before: before, After: after})
		}
	}
	for _, entry := range []struct{ path, template string }{{connectionPath, connectionTemplate}, {callbackPath, callbackTemplate}} {
		if files[entry.path] == "" {
			s.plan.Files = append(s.plan.Files, WorkflowFile{Path: entry.path, After: entry.template})
		} else if files[entry.path] != entry.template {
			return problem("An existing connection-test file differs from the supported template; review it manually.")
		}
	}
	s.plan.Supported = true
	sort.Slice(s.plan.Files, func(i, j int) bool { return s.plan.Files[i].Path < s.plan.Files[j].Path })
	encoded, _ := json.Marshal(struct {
		Repository Repository
		Base, Tree string
		Files      map[string]string
		Changes    []WorkflowFile
	}{repo, s.sha, s.tree, files, s.plan.Files})
	digest := sha256.Sum256(encoded)
	s.plan.Fingerprint = hex.EncodeToString(digest[:])
	return s, nil
}
