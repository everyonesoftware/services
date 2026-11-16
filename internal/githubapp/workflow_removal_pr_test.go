package githubapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRemoveWorkflowRejectsStaleApproval(t *testing.T) {
	app, _ := testApp(t)
	sha := baseSHA
	files := fixtureFiles(t)
	c := fakeClient(t, planTransport(t, files, &sha, nil))
	plan, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
	if err != nil || !plan.Supported {
		t.Fatalf("%+v %v", plan, err)
	}
	sha = strings.Repeat("c", 40)
	if _, err = c.RemoveWorkflow(context.Background(), app, repository, plan.Fingerprint); err == nil {
		t.Fatal("stale approval accepted")
	}
}

func TestRemoveWorkflowPRExactReadbackAndDeletion(t *testing.T) {
	for _, conflict := range []string{"", "foreign", "deleted-file-present", "extra-file", "closed-pr", "changed-branch"} {
		t.Run(conflict, func(t *testing.T) {
			app, _ := testApp(t)
			sha := baseSHA
			files := fixtureFiles(t)
			c := fakeClient(t, planTransport(t, files, &sha, nil))
			plan, err := c.PlanWorkflowRemoval(context.Background(), app, repository)
			if err != nil || !plan.Supported {
				t.Fatalf("%+v %v", plan, err)
			}
			branch := "services/publisher-removal/" + plan.Fingerprint
			marker := "Services publisher removal plan " + plan.Fingerprint
			commitSHA := strings.Repeat("d", 40)
			newTree := strings.Repeat("e", 40)
			exists, prExists := conflict == "foreign", false
			writes, branchReads, prReads, compareReads := 0, 0, 0, 0
			body := ""
			pr := func() map[string]any {
				state := "open"
				if conflict == "closed-pr" {
					state = "closed"
				}
				return map[string]any{"number": 17, "html_url": "https://github.com/owner/repo/pull/17", "state": state, "body": body, "head": map[string]any{"ref": branch, "sha": commitSHA, "repo": map[string]any{"id": repository.ID}}, "base": map[string]any{"ref": "main", "repo": map[string]any{"id": repository.ID}}}
			}
			c = fakeClient(t, planTransport(t, files, &sha, func(r *http.Request) (*http.Response, error) {
				p := strings.TrimPrefix(r.URL.Path, "/repos/owner/repo")
				if r.Method != "GET" {
					writes++
				}
				switch {
				case r.Method == "GET" && strings.HasPrefix(p, "/git/ref/heads/"):
					if !strings.Contains(p, branch) {
						t.Fatal("wrong branch", p)
					}
					branchReads++
					if !exists {
						return response(404, nil), nil
					}
					actual := commitSHA
					if conflict == "changed-branch" && branchReads >= 3 {
						actual = strings.Repeat("f", 40)
					}
					return response(200, map[string]any{"ref": "refs/heads/" + branch, "object": map[string]string{"type": "commit", "sha": actual}}), nil
				case r.Method == "POST" && p == "/git/trees":
					var v struct {
						Base string `json:"base_tree"`
						Tree []map[string]any
					}
					if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
						t.Fatal(err)
					}
					if v.Base != treeSHA || len(v.Tree) != len(plan.Files) {
						t.Fatal("wrong tree", v)
					}
					expected := map[string]WorkflowFile{}
					for _, f := range plan.Files {
						expected[f.Path] = f
					}
					for _, entry := range v.Tree {
						path, _ := entry["path"].(string)
						f, ok := expected[path]
						if !ok {
							t.Fatal("unexpected file", path)
						}
						delete(expected, path)
						if entry["mode"] != "100644" || entry["type"] != "blob" {
							t.Fatal(entry)
						}
						if f.After == "" {
							value, present := entry["sha"]
							if !present || value != nil {
								t.Fatal("deletion must use sha:null", entry)
							}
							if _, ok := entry["content"]; ok {
								t.Fatal("deletion has content")
							}
						} else if entry["content"] != f.After {
							t.Fatal("wrong content")
						}
					}
					if len(expected) != 0 {
						t.Fatal("missing files")
					}
					return response(201, map[string]string{"sha": newTree}), nil
				case r.Method == "POST" && p == "/git/commits":
					var v struct {
						Message, Tree string
						Parents       []string
					}
					json.NewDecoder(r.Body).Decode(&v)
					if v.Message != marker || v.Tree != newTree || len(v.Parents) != 1 || v.Parents[0] != baseSHA {
						t.Fatal("unsafe commit", v)
					}
					return response(201, map[string]string{"sha": commitSHA}), nil
				case r.Method == "POST" && p == "/git/refs":
					var v struct{ Ref, SHA string }
					json.NewDecoder(r.Body).Decode(&v)
					if exists || v.Ref != "refs/heads/"+branch || v.SHA != commitSHA {
						t.Fatal("unsafe ref", v)
					}
					exists = true
					return response(201, nil), nil
				case r.Method == "GET" && p == "/git/commits/"+commitSHA:
					message := marker
					if conflict == "foreign" {
						message = "someone else's work"
					}
					return response(200, map[string]any{"sha": commitSHA, "message": message, "tree": map[string]string{"sha": newTree}, "parents": []map[string]string{{"sha": baseSHA}}}), nil
				case r.Method == "GET" && strings.HasPrefix(p, "/contents/"):
					if r.URL.Query().Get("ref") != commitSHA {
						t.Fatal("not immutable")
					}
					for _, f := range plan.Files {
						if p == "/contents/"+f.Path {
							if f.After == "" {
								if conflict != "deleted-file-present" {
									return response(404, nil), nil
								}
								return response(200, map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(f.Before))}), nil
							}
							return response(200, map[string]string{"type": "file", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(f.After))}), nil
						}
					}
					t.Fatal("unexpected content", p)
				case r.Method == "GET" && p == "/compare/"+baseSHA+"..."+commitSHA:
					compareReads++
					changed := []map[string]string{}
					for _, f := range plan.Files {
						status := "modified"
						if f.After == "" {
							status = "removed"
						}
						changed = append(changed, map[string]string{"filename": f.Path, "status": status})
					}
					if conflict == "extra-file" {
						changed = append(changed, map[string]string{"filename": "unapproved.txt", "status": "added"})
					}
					return response(200, map[string]any{"status": "ahead", "ahead_by": 1, "behind_by": 0, "total_commits": 1, "files": changed}), nil
				case r.Method == "GET" && p == "/pulls":
					if r.URL.Query().Get("head") != "owner:"+branch || r.URL.Query().Get("base") != "main" {
						t.Fatal("unscoped discovery")
					}
					if prExists {
						return response(200, []any{pr()}), nil
					}
					return response(200, []any{}), nil
				case r.Method == "POST" && p == "/pulls":
					var v struct {
						Title, Head, Base, Body    string
						Draft, MaintainerCanModify bool `json:"-"`
					}
					var raw map[string]any
					json.NewDecoder(r.Body).Decode(&raw)
					encoded, _ := json.Marshal(raw)
					json.Unmarshal(encoded, &v)
					if v.Title != "Remove Services publication notifications" || v.Head != branch || v.Base != "main" || raw["draft"] != false || raw["maintainer_can_modify"] != false || !strings.HasPrefix(v.Body, marker+"\n") || !strings.Contains(v.Body, "Tailscale") {
						t.Fatal("wrong removal PR", raw)
					}
					body = v.Body
					prExists = true
					return response(201, pr()), nil
				case r.Method == "GET" && p == "/pulls/17":
					prReads++
					return response(200, pr()), nil
				default:
					t.Fatalf("unexpected %s %s", r.Method, r.URL)
				}
				return nil, nil
			}))
			url, err := c.RemoveWorkflow(context.Background(), app, repository, plan.Fingerprint)
			if conflict != "" {
				if err == nil {
					t.Fatal("conflict accepted", conflict)
				}
				if conflict == "foreign" && writes != 0 {
					t.Fatal("wrote over foreign branch")
				}
				return
			}
			if err != nil || url != "https://github.com/owner/repo/pull/17" || writes != 4 || prReads == 0 || compareReads == 0 {
				t.Fatalf("%s %v writes=%d prReads=%d compare=%d", url, err, writes, prReads, compareReads)
			}
			before := writes
			if again, err := c.RemoveWorkflow(context.Background(), app, repository, plan.Fingerprint); err != nil || again != url || writes != before {
				t.Fatalf("not idempotent %q %v writes=%d", again, err, writes)
			}
		})
	}
}
