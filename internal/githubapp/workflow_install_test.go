package githubapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestInstallRejectsStaleApprovalBeforeAnyRepositoryWrite(t *testing.T) {
	app, _ := testApp(t)
	sha := baseSHA
	files := fixtureFiles(t)
	delete(files, connectionPath)
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil {
		t.Fatal(err)
	}
	sha = strings.Repeat("c", 40)
	if _, err = c.InstallWorkflow(context.Background(), app, repository, plan.Fingerprint); err == nil {
		t.Fatal("stale approval accepted")
	}
}

func TestInstallUsesDedicatedPRAndVerifiesExactReadback(t *testing.T) {
	app, _ := testApp(t)
	sha := baseSHA
	files := fixtureFiles(t)
	delete(files, connectionPath)
	delete(files, callbackPath)
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil {
		t.Fatal(err)
	}
	branch := "services/publisher-onboarding/" + plan.Fingerprint
	commitSHA := strings.Repeat("d", 40)
	newTree := strings.Repeat("e", 40)
	branchExists := false
	prExists := false
	writes := 0
	prReads := 0
	contentReads := 0
	compareReads := 0
	message := ""
	prBody := ""
	targetFiles := map[string]string{}
	for _, f := range plan.Files {
		targetFiles[f.Path] = f.After
	}
	pr := func() map[string]any {
		return map[string]any{"number": 17, "html_url": "https://github.com/owner/repo/pull/17", "state": "open", "body": prBody, "head": map[string]any{"ref": branch, "sha": commitSHA, "repo": map[string]any{"id": repository.ID}}, "base": map[string]any{"ref": "main", "repo": map[string]any{"id": repository.ID}}}
	}
	c = fakeClient(t, planTransport(t, files, &sha, func(r *http.Request) (*http.Response, error) {
		p := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo")
		if r.Method != "GET" {
			writes++
		}
		switch {
		case r.Method == "GET" && strings.HasPrefix(p, "/git/ref/heads/"):
			if !strings.Contains(p, branch) {
				t.Fatalf("wrong branch %s", p)
			}
			if !branchExists {
				return response(404, nil), nil
			}
			return response(200, map[string]any{"ref": "refs/heads/" + branch, "object": map[string]string{"type": "commit", "sha": commitSHA}}), nil
		case r.Method == "POST" && p == "/git/trees":
			var v struct {
				Base string `json:"base_tree"`
				Tree []struct{ Path, Mode, Type, Content string }
			}
			if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
				t.Fatal(err)
			}
			if v.Base != treeSHA || len(v.Tree) != len(plan.Files) {
				t.Fatalf("wrong tree %+v", v)
			}
			for _, f := range v.Tree {
				if f.Mode != "100644" || f.Type != "blob" || targetFiles[f.Path] != f.Content {
					t.Fatal("wrong file", f.Path)
				}
			}
			return response(201, map[string]string{"sha": newTree}), nil
		case r.Method == "POST" && p == "/git/commits":
			var v struct {
				Message, Tree string
				Parents       []string
			}
			json.NewDecoder(r.Body).Decode(&v)
			if v.Tree != newTree || len(v.Parents) != 1 || v.Parents[0] != baseSHA {
				t.Fatal("wrong commit base")
			}
			message = v.Message
			return response(201, map[string]string{"sha": commitSHA}), nil
		case r.Method == "POST" && p == "/git/refs":
			var v struct{ Ref, SHA string }
			json.NewDecoder(r.Body).Decode(&v)
			if v.Ref != "refs/heads/"+branch || v.SHA != commitSHA || branchExists {
				t.Fatal("unsafe ref write")
			}
			branchExists = true
			return response(201, map[string]any{"ref": v.Ref, "object": map[string]string{"type": "commit", "sha": v.SHA}}), nil
		case r.Method == "GET" && p == "/git/commits/"+commitSHA:
			return response(200, map[string]any{"sha": commitSHA, "message": message, "tree": map[string]string{"sha": newTree}, "parents": []map[string]string{{"sha": baseSHA}}}), nil
		case r.Method == "GET" && p == "/pulls":
			if r.URL.Query().Get("head") != "owner:"+branch || r.URL.Query().Get("base") != "main" {
				t.Fatal("unscoped PR discovery")
			}
			if prExists {
				return response(200, []any{pr()}), nil
			}
			return response(200, []any{}), nil
		case r.Method == "POST" && p == "/pulls":
			var v struct {
				Head, Base, Body string
				Draft            bool
			}
			json.NewDecoder(r.Body).Decode(&v)
			if v.Head != branch || v.Base != "main" || v.Draft {
				t.Fatal("wrong PR target")
			}
			prBody = v.Body
			prExists = true
			return response(201, pr()), nil
		case r.Method == "GET" && p == "/pulls/17":
			prReads++
			return response(200, pr()), nil
		case r.Method == "GET" && p == "/compare/"+baseSHA+"..."+commitSHA:
			compareReads++
			changed := []map[string]string{}
			for _, f := range plan.Files {
				changed = append(changed, map[string]string{"filename": f.Path, "status": "added"})
			}
			return response(200, map[string]any{"status": "ahead", "ahead_by": 1, "behind_by": 0, "total_commits": 1, "files": changed}), nil
		case r.Method == "GET" && strings.HasPrefix(p, "/contents/"):
			if r.URL.Query().Get("ref") != commitSHA {
				t.Fatal("readback not immutable")
			}
			contentReads++
			s := targetFiles[strings.TrimPrefix(p, "/contents/")]
			return response(200, map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(s))}), nil
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL)
			return nil, nil
		}
	}))
	url, err := c.InstallWorkflow(context.Background(), app, repository, plan.Fingerprint)
	if err != nil || url != "https://github.com/owner/repo/pull/17" || writes != 4 || prReads == 0 || contentReads != len(plan.Files) || compareReads == 0 {
		t.Fatalf("%s %v writes=%d prReads=%d contents=%d compare=%d", url, err, writes, prReads, contentReads, compareReads)
	}
	before := writes
	again, err := c.InstallWorkflow(context.Background(), app, repository, plan.Fingerprint)
	if err != nil || again != url || writes != before {
		t.Fatalf("rerun not idempotent: %s %v writes=%d", again, err, writes)
	}
}

func TestInstallDoesNotOverwriteExistingUnownedBranch(t *testing.T) {
	app, _ := testApp(t)
	sha := baseSHA
	files := fixtureFiles(t)
	delete(files, connectionPath)
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflow(context.Background(), app, repository)
	if err != nil {
		t.Fatal(err)
	}
	branch := "services/publisher-onboarding/" + plan.Fingerprint
	foreign := strings.Repeat("d", 40)
	c = fakeClient(t, planTransport(t, files, &sha, func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("wrote over foreign branch")
		}
		if strings.Contains(r.URL.Path, "/git/ref/heads/") {
			return response(200, map[string]any{"ref": "refs/heads/" + branch, "object": map[string]string{"type": "commit", "sha": foreign}}), nil
		}
		if strings.HasSuffix(r.URL.Path, "/git/commits/"+foreign) {
			return response(200, map[string]any{"sha": foreign, "message": "someone else's work", "parents": []map[string]string{{"sha": baseSHA}}, "tree": map[string]string{"sha": treeSHA}}), nil
		}
		t.Fatal("unexpected request", r.URL)
		return nil, nil
	}))
	if _, err = c.InstallWorkflow(context.Background(), app, repository, plan.Fingerprint); err == nil {
		t.Fatal("foreign branch accepted")
	}
}
