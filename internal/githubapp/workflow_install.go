package githubapp

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// InstallWorkflow re-plans from the actual default branch before any repository
// mutation. Only an immutable commit and a new, fingerprint-named ref are ever
// created: default-branch writes and force updates are deliberately unavailable.
// A partial failure leaves an owned branch that can be resumed, never deleted or
// overwritten. Concurrent creates can return a safe conflict; retry re-verifies.
func (c *Client) InstallWorkflow(ctx context.Context, app App, repo Repository, fingerprint string) (string, error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(fingerprint) {
		return "", errors.New("invalid workflow approval fingerprint")
	}
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repo, map[string]string{"metadata": "read", "contents": "write", "workflows": "write", "pull_requests": "write"})
	if err != nil {
		return "", err
	}
	snapshot, err := o.planWorkflow(repo, token)
	if err != nil {
		return "", err
	}
	if !snapshot.plan.Supported || snapshot.plan.Fingerprint != fingerprint {
		return "", errors.New("workflow approval is stale or unsupported; review a fresh plan")
	}
	if len(snapshot.plan.Files) == 0 {
		return "", errors.New("workflow is already installed on the default branch; no pull request is needed")
	}
	base := "/repos/" + repo.FullName
	branch := "services/publisher-onboarding/" + fingerprint
	marker := "Services publisher onboarding plan " + fingerprint
	ref, status, err := o.workflowRef(repo, token, branch)
	if err != nil {
		return "", err
	}
	if status == 404 {
		// Narrow the race between re-planning and the first mutation. The created
		// commit still has exactly the approved base as its sole parent.
		current, code, e := o.workflowRef(repo, token, repo.DefaultBranch)
		if e != nil {
			return "", e
		}
		if code != 200 || current.Object.SHA != snapshot.sha {
			return "", errors.New("workflow default branch changed; review a fresh plan")
		}
		entries := []map[string]string{}
		for _, file := range snapshot.plan.Files {
			entries = append(entries, map[string]string{"path": file.Path, "mode": "100644", "type": "blob", "content": file.After})
		}
		var tree struct {
			SHA string `json:"sha"`
		}
		if _, err = o.request("POST", base+"/git/trees", token, map[string]any{"base_tree": snapshot.tree, "tree": entries}, &tree, 201); err != nil {
			return "", err
		}
		if !gitSHA.MatchString(tree.SHA) {
			return "", errors.New("GitHub returned invalid workflow tree")
		}
		var commit struct {
			SHA string `json:"sha"`
		}
		if _, err = o.request("POST", base+"/git/commits", token, map[string]any{"message": marker, "tree": tree.SHA, "parents": []string{snapshot.sha}}, &commit, 201); err != nil {
			return "", err
		}
		if !gitSHA.MatchString(commit.SHA) {
			return "", errors.New("GitHub returned invalid workflow commit")
		}
		// GitHub rejects an existing ref; never use the update-reference endpoint.
		if _, err = o.request("POST", base+"/git/refs", token, map[string]string{"ref": "refs/heads/" + branch, "sha": commit.SHA}, nil, 201); err != nil {
			return "", err
		}
		ref, status, err = o.workflowRef(repo, token, branch)
		if err != nil {
			return "", err
		}
		if status != 200 || ref.Object.SHA != commit.SHA {
			return "", errors.New("GitHub workflow branch readback did not match")
		}
	}
	if err = o.verifyWorkflowBranch(repo, token, branch, ref.Object.SHA, marker, snapshot); err != nil {
		return "", err
	}
	// Find an existing owned PR, including closed ones. A closed/modified PR is a
	// safe conflict, not permission to recreate or overwrite somebody else's work.
	var prs []workflowPR
	owner := strings.SplitN(repo.FullName, "/", 2)[0]
	path := base + "/pulls?state=all&per_page=100&head=" + url.QueryEscape(owner+":"+branch) + "&base=" + url.QueryEscape(repo.DefaultBranch)
	if _, err = o.request("GET", path, token, nil, &prs, 200); err != nil {
		return "", err
	}
	if prs == nil || len(prs) > 1 || o.next {
		return "", ErrConflict
	}
	var pr workflowPR
	if len(prs) == 1 {
		pr = prs[0]
	} else {
		body := marker + "\n\nReview these workflow and sender changes before merging. Notifications remain opt-in and do not deploy images. Configure private-network federation and verify the authenticated connection callback before enabling delivery. Notification-only retries reuse the original retained publication payload."
		if _, err = o.request("POST", base+"/pulls", token, map[string]any{"title": "Connect Services publication notifications", "body": body, "head": branch, "base": repo.DefaultBranch, "draft": false, "maintainer_can_modify": false}, &pr, 201); err != nil {
			return "", err
		}
	}
	if !pr.matches(repo, branch, ref.Object.SHA, marker) {
		return "", ErrConflict
	}
	var readback workflowPR
	if _, err = o.request("GET", base+"/pulls/"+strconv.FormatInt(pr.Number, 10), token, nil, &readback, 200); err != nil {
		return "", err
	}
	if !readback.matches(repo, branch, ref.Object.SHA, marker) || readback.Number != pr.Number {
		return "", errors.New("GitHub workflow pull request readback did not match")
	}
	// Re-read the exact branch after PR creation; an outside update must not be
	// reported as the approved change just because PR creation returned success.
	final, code, err := o.workflowRef(repo, token, branch)
	if err != nil {
		return "", err
	}
	if code != 200 || final.Object.SHA != ref.Object.SHA {
		return "", errors.New("GitHub workflow branch changed during installation")
	}
	return readback.URL, nil
}

func (o *operation) verifyWorkflowBranch(repo Repository, token, branch, sha, marker string, s workflowSnapshot) error {
	var commit struct {
		SHA     string `json:"sha"`
		Message string `json:"message"`
		Tree    struct {
			SHA string `json:"sha"`
		} `json:"tree"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
	}
	if _, err := o.request("GET", "/repos/"+repo.FullName+"/git/commits/"+sha, token, nil, &commit, 200); err != nil {
		return err
	}
	if commit.SHA != sha || commit.Message != marker || len(commit.Parents) != 1 || commit.Parents[0].SHA != s.sha || !gitSHA.MatchString(commit.Tree.SHA) {
		return ErrConflict
	}
	for _, file := range s.plan.Files {
		text, exists, err := o.workflowContent(repo, token, file.Path, sha)
		if err != nil {
			return err
		}
		if !exists || text != file.After {
			return errors.New("GitHub workflow content readback did not match")
		}
	}
	// A fingerprint marker alone is not ownership: verify the commit changes
	// exactly the approved paths and contains no extra edits hidden in the PR.
	var comparison struct {
		Status string `json:"status"`
		Ahead  int    `json:"ahead_by"`
		Behind int    `json:"behind_by"`
		Total  int    `json:"total_commits"`
		Files  []struct {
			Path   string `json:"filename"`
			Status string `json:"status"`
		} `json:"files"`
	}
	if _, err := o.request("GET", "/repos/"+repo.FullName+"/compare/"+s.sha+"..."+sha, token, nil, &comparison, 200); err != nil {
		return err
	}
	if o.next || comparison.Status != "ahead" || comparison.Ahead != 1 || comparison.Behind != 0 || comparison.Total != 1 || len(comparison.Files) != len(s.plan.Files) {
		return ErrConflict
	}
	expected := map[string]string{}
	for _, file := range s.plan.Files {
		status := "modified"
		if file.Before == "" {
			status = "added"
		}
		expected[file.Path] = status
	}
	for _, file := range comparison.Files {
		if expected[file.Path] != file.Status || file.Status == "" {
			return ErrConflict
		}
		delete(expected, file.Path)
	}
	if len(expected) != 0 {
		return ErrConflict
	}
	return nil
}

type workflowPR struct {
	Number int64  `json:"number"`
	URL    string `json:"html_url"`
	State  string `json:"state"`
	Body   string `json:"body"`
	Head   struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"base"`
}

func (p workflowPR) matches(repo Repository, branch, sha, marker string) bool {
	return p.Number > 0 && p.URL == "https://github.com/"+repo.FullName+"/pull/"+strconv.FormatInt(p.Number, 10) && p.State == "open" && strings.HasPrefix(p.Body, marker+"\n") && p.Head.Ref == branch && p.Head.SHA == sha && p.Head.Repo.ID == repo.ID && p.Base.Ref == repo.DefaultBranch && p.Base.Repo.ID == repo.ID
}
