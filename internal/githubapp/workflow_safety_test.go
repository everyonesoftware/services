package githubapp

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// A stateful injected GitHub transport; no real credentials or writes are used.
type installRemote struct {
	t        *testing.T
	files    map[string]string
	sha      string
	plan     WorkflowPlan
	branch   string
	exists   bool
	message  string
	body     string
	prExists bool
	tamper   string
}

func (s *installRemote) transport() transportFunc {
	return planTransport(s.t, s.files, &s.sha, func(r *http.Request) (*http.Response, error) {
		p := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo")
		head := strings.Repeat("d", 40)
		tree := strings.Repeat("e", 40)
		pr := func() map[string]any {
			u := "https://github.com/owner/repo/pull/17"
			if s.tamper == "url" {
				u = "https://evil.example/stolen"
			}
			branch := s.branch
			if s.tamper == "head" {
				branch = "main"
			}
			return map[string]any{"number": 17, "html_url": u, "state": "open", "body": s.body, "head": map[string]any{"ref": branch, "sha": head, "repo": map[string]any{"id": repository.ID}}, "base": map[string]any{"ref": "main", "repo": map[string]any{"id": repository.ID}}}
		}
		switch {
		case r.Method == "GET" && strings.Contains(p, "/git/ref/heads/"):
			if !s.exists {
				return response(404, nil), nil
			}
			return response(200, map[string]any{"ref": "refs/heads/" + s.branch, "object": map[string]string{"type": "commit", "sha": head}}), nil
		case r.Method == "POST" && p == "/git/trees":
			return response(201, map[string]string{"sha": tree}), nil
		case r.Method == "POST" && p == "/git/commits":
			s.message = "Services publisher onboarding plan " + s.plan.Fingerprint
			return response(201, map[string]string{"sha": head}), nil
		case r.Method == "POST" && p == "/git/refs":
			s.exists = true
			return response(201, nil), nil
		case r.Method == "GET" && p == "/git/commits/"+head:
			return response(200, map[string]any{"sha": head, "message": s.message, "tree": map[string]string{"sha": tree}, "parents": []map[string]string{{"sha": s.sha}}}), nil
		case r.Method == "GET" && strings.HasPrefix(p, "/contents/"):
			path := strings.TrimPrefix(p, "/contents/")
			after := ""
			for _, f := range s.plan.Files {
				if f.Path == path {
					after = f.After
				}
			}
			if s.tamper == "content" {
				after += "\n// changed after approval"
			}
			return response(200, map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(after))}), nil
		case r.Method == "GET" && strings.HasPrefix(p, "/compare/"):
			changed := []map[string]string{}
			for _, f := range s.plan.Files {
				status := "modified"
				if f.Before == "" {
					status = "added"
				}
				changed = append(changed, map[string]string{"filename": f.Path, "status": status})
			}
			if s.tamper == "extra-change" {
				changed = append(changed, map[string]string{"filename": "README.md", "status": "modified"})
			}
			return response(200, map[string]any{"status": "ahead", "ahead_by": 1, "behind_by": 0, "total_commits": 1, "files": changed}), nil
		case r.Method == "GET" && p == "/pulls":
			if s.prExists {
				return response(200, []any{pr()}), nil
			}
			return response(200, []any{}), nil
		case r.Method == "POST" && p == "/pulls":
			s.body = s.message + "\n\nReview before merging."
			s.prExists = true
			return response(201, pr()), nil
		case r.Method == "GET" && p == "/pulls/17":
			return response(200, pr()), nil
		default:
			s.t.Fatalf("unexpected %s %s", r.Method, p)
			return nil, nil
		}
	})
}
func newInstallRemote(t *testing.T, files map[string]string, plan WorkflowPlan) *installRemote {
	return &installRemote{t: t, files: files, sha: baseSHA, plan: plan, branch: "services/publisher-onboarding/" + plan.Fingerprint}
}

func TestInstallAPIFailuresAreBoundedSanitizedAndNeverReportSuccess(t *testing.T) {
	app, _ := testApp(t)
	files := fixtureFiles(t)
	delete(files, connectionPath)
	delete(files, callbackPath)
	sha := baseSHA
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil {
		t.Fatal(err)
	}
	baseline := newInstallRemote(t, files, plan)
	transport := baseline.transport()
	calls := 0
	c = fakeClient(t, func(r *http.Request) (*http.Response, error) { calls++; return transport(r) })
	if _, err = c.InstallWorkflow(context.Background(), app, repository, plan.Fingerprint); err != nil {
		t.Fatal(err)
	}
	for failAt := 1; failAt <= calls; failAt++ {
		remote := newInstallRemote(t, files, plan)
		transport := remote.transport()
		seen := 0
		c = fakeClient(t, func(r *http.Request) (*http.Response, error) {
			seen++
			if seen == failAt {
				return response(503, map[string]string{"message": "sensitive-token-provider-body"}), nil
			}
			return transport(r)
		})
		u, e := c.InstallWorkflow(context.Background(), app, repository, plan.Fingerprint)
		if e == nil || u != "" || seen != failAt || len(e.Error()) > 200 || strings.Contains(e.Error(), "sensitive") {
			t.Fatalf("failure %d: url=%q err=%v calls=%d", failAt, u, e, seen)
		}
	}
	for _, tamper := range []string{"content", "extra-change", "url", "head"} {
		remote := newInstallRemote(t, files, plan)
		remote.tamper = tamper
		c = fakeClient(t, remote.transport())
		u, e := c.InstallWorkflow(context.Background(), app, repository, plan.Fingerprint)
		if e == nil || u != "" {
			t.Fatalf("accepted %s tampering", tamper)
		}
	}
}

func TestPlanRejectsOversizedProviderResponsesAndSanitizesTransportErrors(t *testing.T) {
	app, _ := testApp(t)
	for _, oversize := range []bool{false, true} {
		calls := 0
		c := fakeClient(t, repositoryTransport(t, func(*http.Request) (*http.Response, error) {
			calls++
			if !oversize {
				return nil, errors.New("sensitive-transport-credential")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxBody+1)))}, nil
		}))
		_, err := c.PlanWorkflow(context.Background(), app, repository)
		if err == nil || strings.Contains(err.Error(), "sensitive") || len(err.Error()) > 200 || calls != 1 {
			t.Fatal("unbounded/unsafe failure", err, calls)
		}
	}
}

func TestWorkflowFingerprintBindsBeforeBytesAndRefusesStaleContent(t *testing.T) {
	app, _ := testApp(t)
	files := fixtureFiles(t)
	delete(files, connectionPath)
	sha := baseSHA
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	a, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil {
		t.Fatal(err)
	}
	files[senderPath] += "\n// Reviewed source changed\n"
	b, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil || a.Fingerprint == b.Fingerprint {
		t.Fatal("before bytes not fingerprinted", err)
	}
	if _, err = c.InstallWorkflow(context.Background(), app, repository, a.Fingerprint); err == nil {
		t.Fatal("stale content approval accepted")
	}
}
