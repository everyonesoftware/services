package githubapp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/nacl/box"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

type repairProvider struct {
	variables map[string]string
	secret    bool
	updated   string
	writes    []string
	readbacks map[string]int
	ignore    string
	failure   string
}

func newRepairProvider(t *testing.T) (*repairProvider, *Client) {
	t.Helper()
	p := &repairProvider{variables: map[string]string{enabledName: "true", "TAILSCALE_CLIENT_ID": "old-client", "TAILSCALE_TAGS": "tag:services", "UNRELATED": "retain"}, secret: true, updated: "2026-01-01T00:00:00Z", readbacks: map[string]int{}}
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		path := r.URL.Path
		name := path[strings.LastIndex(path, "/")+1:]
		if p.failure != "" {
			return response(403, map[string]string{"message": p.failure}), nil
		}
		if path == "/repos/owner/repo/actions/secrets/public-key" && r.Method == "GET" {
			return response(200, map[string]string{"key_id": "key-1", "key": base64.StdEncoding.EncodeToString(pub[:])}), nil
		}
		if path == "/repos/owner/repo/actions/secrets/"+secretName {
			if r.Method == "GET" {
				if !p.secret {
					return response(404, nil), nil
				}
				p.readbacks[secretName]++
				return response(200, map[string]string{"name": secretName, "created_at": "2025-01-01T00:00:00Z", "updated_at": p.updated}), nil
			}
			if r.Method != "PUT" {
				t.Fatalf("unexpected secret operation %s", r.Method)
			}
			if p.variables[enabledName] != "false" || p.readbacks[enabledName] < 3 {
				t.Fatal("credential swap before verified disable")
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			cipher, err := base64.StdEncoding.DecodeString(body["encrypted_value"])
			if err != nil {
				t.Fatal(err)
			}
			plain, ok := box.OpenAnonymous(nil, cipher, pub, priv)
			if !ok || string(plain) != strings.Repeat("s", 32) || body["key_id"] != "key-1" {
				t.Fatal("incorrect encrypted credential")
			}
			p.writes = append(p.writes, secretName)
			p.secret = p.ignore != secretName
			p.updated = "2026-02-01T00:00:00Z"
			return response(204, nil), nil
		}
		if strings.HasPrefix(path, "/repos/owner/repo/actions/variables") {
			if r.Method == "GET" {
				if !variableAllowlist[name] {
					t.Fatalf("unrelated read %s", path)
				}
				p.readbacks[name]++
				value, exists := p.variables[name]
				if !exists {
					return response(404, nil), nil
				}
				return response(200, variable{Name: name, Value: value}), nil
			}
			var body variable
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if !variableAllowlist[body.Name] {
				t.Fatalf("unrelated write %+v", body)
			}
			_, exists := p.variables[body.Name]
			if exists && (r.Method != "PATCH" || name != body.Name) || !exists && (r.Method != "POST" || name != "variables") {
				t.Fatalf("wrong variable operation %s %s", r.Method, path)
			}
			if len(p.writes) == 0 && (body.Name != enabledName || body.Value != "false") {
				t.Fatal("first write did not disable notifications")
			}
			p.writes = append(p.writes, body.Name)
			if p.ignore != body.Name {
				p.variables[body.Name] = body.Value
			}
			if exists {
				return response(204, nil), nil
			}
			return response(201, nil), nil
		}
		t.Fatalf("unrelated operation: %s %s", r.Method, path)
		return nil, nil
	}))
	return p, c
}

func TestConfigurationRepairReplacesOnlyReviewedConfigurationWithReadbacks(t *testing.T) {
	app, _ := testApp(t)
	p, c := newRepairProvider(t)
	desired := repairVariables()
	plan, err := c.PlanConfiguration(context.Background(), app, repository, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.ApplyConfigurationRepair(context.Background(), app, repository, strings.Repeat("s", 32), desired, plan.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if len(p.writes) != 5 || p.writes[0] != enabledName || !p.secret {
		t.Fatalf("writes: %v", p.writes)
	}
	for name, want := range desired {
		if p.variables[name] != want || p.readbacks[name] < 3 {
			t.Fatalf("unverified %s: %q reads=%d", name, p.variables[name], p.readbacks[name])
		}
	}
	if p.variables["UNRELATED"] != "retain" || p.readbacks[secretName] < 3 {
		t.Fatalf("unrelated state or missing secret readback: %+v", p)
	}
}

func TestConfigurationRepairRejectsStaleReviewBeforeAnyWrite(t *testing.T) {
	app, _ := testApp(t)
	for _, change := range []string{"secret-updated", "secret-removed", "variable-value", "variable-missing", "desired", "app-id", "app-slug", "fingerprint"} {
		t.Run(change, func(t *testing.T) {
			p, c := newRepairProvider(t)
			desired := repairVariables()
			reviewedApp := app
			plan, err := c.PlanConfiguration(context.Background(), app, repository, desired)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "secret-updated":
				p.updated = "2026-03-01T00:00:00Z"
			case "secret-removed":
				p.secret = false
			case "variable-value":
				p.variables["TAILSCALE_TAGS"] = "other"
			case "variable-missing":
				delete(p.variables, "TAILSCALE_TAGS")
			case "desired":
				desired["TAILSCALE_TAGS"] = "tag:new"
			case "app-id":
				reviewedApp.ID++
			case "app-slug":
				reviewedApp.Slug = "another-app"
			case "fingerprint":
				plan.Fingerprint = strings.Repeat("0", 64)
			}
			err = c.ApplyConfigurationRepair(context.Background(), reviewedApp, repository, strings.Repeat("s", 32), desired, plan.Fingerprint)
			if !errors.Is(err, ErrConflict) || len(p.writes) != 0 {
				t.Fatalf("stale approval: err=%v writes=%v", err, p.writes)
			}
		})
	}
}

func TestConfigurationRepairRejectsInvalidInputWithoutRequests(t *testing.T) {
	app, _ := testApp(t)
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("request on invalid input: %s %s", r.Method, r.URL.Path)
		return response(500, nil), nil
	})
	for _, kind := range []string{"missing", "extra", "wrong-name", "enabled", "oversized", "nul", "nil"} {
		t.Run("variables/"+kind, func(t *testing.T) {
			vars := repairVariables()
			switch kind {
			case "missing":
				delete(vars, "TAILSCALE_TAGS")
			case "extra":
				vars["OTHER"] = "x"
			case "wrong-name":
				delete(vars, "TAILSCALE_TAGS")
				vars["OTHER"] = "x"
			case "enabled":
				vars[enabledName] = "true"
			case "oversized":
				vars["TAILSCALE_TAGS"] = strings.Repeat("x", (48<<10)+1)
			case "nul":
				vars["TAILSCALE_TAGS"] = "tag:\x00bad"
			case "nil":
				vars = nil
			}
			if _, err := c.PlanConfiguration(context.Background(), app, repository, vars); err == nil {
				t.Fatal("accepted invalid plan")
			}
			if err := c.ApplyConfigurationRepair(context.Background(), app, repository, strings.Repeat("s", 32), vars, strings.Repeat("0", 64)); err == nil {
				t.Fatal("accepted invalid repair")
			}
		})
	}
	for _, secret := range []string{"", strings.Repeat("s", 31), strings.Repeat("s", (48<<10)+1), strings.Repeat("s", 32) + "\n", strings.Repeat("s", 32) + " ", strings.Repeat("s", 32) + "é"} {
		if err := c.ApplyConfigurationRepair(context.Background(), app, repository, secret, repairVariables(), strings.Repeat("0", 64)); err == nil {
			t.Fatal("accepted invalid secret")
		}
	}
	for _, fingerprint := range []string{"", strings.Repeat("0", 63), strings.Repeat("0", 65), strings.Repeat("g", 64), strings.Repeat("A", 64)} {
		if err := c.ApplyConfigurationRepair(context.Background(), app, repository, strings.Repeat("s", 32), repairVariables(), fingerprint); err == nil {
			t.Fatal("accepted invalid fingerprint")
		}
	}
}

func TestConfigurationRepairStopsOnFailedReadback(t *testing.T) {
	app, _ := testApp(t)
	for _, name := range []string{enabledName, "SERVICES_PUBLISH_URL", "TAILSCALE_AUDIENCE", "TAILSCALE_CLIENT_ID", "TAILSCALE_TAGS", secretName} {
		t.Run(name, func(t *testing.T) {
			p, c := newRepairProvider(t)
			desired := repairVariables()
			desired["TAILSCALE_TAGS"] = "tag:new"
			plan, err := c.PlanConfiguration(context.Background(), app, repository, desired)
			if err != nil {
				t.Fatal(err)
			}
			p.ignore = name
			err = c.ApplyConfigurationRepair(context.Background(), app, repository, strings.Repeat("s", 32), desired, plan.Fingerprint)
			if err == nil || len(p.writes) == 0 || p.writes[len(p.writes)-1] != name {
				t.Fatalf("continued after failed readback: err=%v writes=%v", err, p.writes)
			}
		})
	}
}

func TestConfigurationRepairCreatesMissingConfiguration(t *testing.T) {
	app, _ := testApp(t)
	p, c := newRepairProvider(t)
	p.variables = map[string]string{"UNRELATED": "retain"}
	p.secret = false
	desired := repairVariables()
	plan, err := c.PlanConfiguration(context.Background(), app, repository, desired)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SecretExists {
		t.Fatal("secret should be absent")
	}
	for _, v := range plan.Variables {
		if v.Exists {
			t.Fatalf("unexpected variable: %+v", v)
		}
	}
	if err = c.ApplyConfigurationRepair(context.Background(), app, repository, strings.Repeat("s", 32), desired, plan.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if len(p.writes) != 6 || p.variables["UNRELATED"] != "retain" {
		t.Fatalf("writes: %v", p.writes)
	}
}

func TestConfigurationErrorsNeverExposeProviderBodies(t *testing.T) {
	app, _ := testApp(t)
	p, c := newRepairProvider(t)
	desired := repairVariables()
	plan, err := c.PlanConfiguration(context.Background(), app, repository, desired)
	if err != nil {
		t.Fatal(err)
	}
	p.failure = "sensitive-provider-body-" + strings.Repeat("s", 32)
	_, err = c.PlanConfiguration(context.Background(), app, repository, desired)
	if err == nil || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), strings.Repeat("s", 32)) {
		t.Fatalf("unsafe planning error: %v", err)
	}
	err = c.ApplyConfigurationRepair(context.Background(), app, repository, strings.Repeat("s", 32), desired, plan.Fingerprint)
	if err == nil || strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), strings.Repeat("s", 32)) || len(p.writes) != 0 {
		t.Fatalf("unsafe apply error: %v", err)
	}
}

func TestConfigurationPlanBindsRepositoryIdentity(t *testing.T) {
	app, _ := testApp(t)
	repo := repository
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/access_tokens") {
			return tokenResponse(), nil
		}
		if r.Method != "GET" {
			t.Fatalf("unexpected operation: %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Path == "/repos/"+repo.FullName {
			return response(200, map[string]any{"id": repo.ID, "full_name": repo.FullName, "default_branch": repo.DefaultBranch}), nil
		}
		return response(404, nil), nil
	})
	original, err := c.PlanConfiguration(context.Background(), app, repo, repairVariables())
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"id", "name", "branch", "installation"} {
		repo = repository
		switch kind {
		case "id":
			repo.ID++
		case "name":
			repo.FullName = "other/repo"
		case "branch":
			repo.DefaultBranch = "other"
		case "installation":
			repo.InstallationID++
		}
		plan, err := c.PlanConfiguration(context.Background(), app, repo, repairVariables())
		if err != nil || plan.Fingerprint == original.Fingerprint {
			t.Fatalf("unbound %s: %+v %v", kind, plan, err)
		}
	}
}

func TestConfigurationPlanAcceptsVariableSizeBoundary(t *testing.T) {
	app, _ := testApp(t)
	_, c := newRepairProvider(t)
	desired := repairVariables()
	desired["TAILSCALE_TAGS"] = strings.Repeat("x", 48<<10)
	if _, err := c.PlanConfiguration(context.Background(), app, repository, desired); err != nil {
		t.Fatal(err)
	}
}

func repairVariables() map[string]string {
	return map[string]string{enabledName: "false", "SERVICES_PUBLISH_URL": "https://services.example/api/images/published", "TAILSCALE_CLIENT_ID": "client-1", "TAILSCALE_AUDIENCE": "https://services.example", "TAILSCALE_TAGS": "tag:services"}
}

func TestConfigurationPlanReadOnlyAllConflictDetails(t *testing.T) {
	app, _ := testApp(t)
	desired := repairVariables()
	reads := map[string]int{}
	c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatalf("configuration write during review: %s %s", r.Method, r.URL.Path)
		}
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		reads[name]++
		if name == secretName {
			return response(200, map[string]string{"name": name, "created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-02-01T00:00:00Z"}), nil
		}
		if _, ok := desired[name]; !ok {
			t.Fatalf("unrelated read: %s", r.URL.Path)
		}
		if name == "TAILSCALE_TAGS" {
			return response(404, nil), nil
		}
		return response(200, variable{Name: name, Value: "old-" + name}), nil
	}))
	plan, err := c.PlanConfiguration(context.Background(), app, repository, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.SecretExists || len(plan.Variables) != 5 {
		t.Fatalf("incomplete conflicts: %+v", plan)
	}
	names := []string{}
	for _, v := range plan.Variables {
		names = append(names, v.Name)
		if v.DesiredValue != desired[v.Name] || v.Exists != (v.Name != "TAILSCALE_TAGS") {
			t.Fatalf("wrong detail: %+v", v)
		}
		want := "old-" + v.Name
		if !v.Exists {
			want = ""
		}
		if v.CurrentValue != want {
			t.Fatalf("current detail: %+v", v)
		}
	}
	if !reflect.DeepEqual(names, []string{enabledName, "SERVICES_PUBLISH_URL", "TAILSCALE_AUDIENCE", "TAILSCALE_CLIENT_ID", "TAILSCALE_TAGS"}) {
		t.Fatalf("unstable order: %v", names)
	}
	digest, e := hex.DecodeString(plan.Fingerprint)
	if e != nil || len(digest) != 32 {
		t.Fatalf("not SHA256: %q", plan.Fingerprint)
	}
	again, e := c.PlanConfiguration(context.Background(), app, repository, desired)
	if e != nil || again.Fingerprint != plan.Fingerprint {
		t.Fatalf("unstable review: %+v %v", again, e)
	}
	b, _ := json.Marshal(plan)
	for _, key := range []string{`"variables"`, `"secretExists"`, `"fingerprint"`, `"name"`, `"exists"`, `"currentValue"`, `"desiredValue"`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("missing JSON %s: %s", key, b)
		}
	}
	if len(reads) != 6 {
		t.Fatalf("reads: %v", reads)
	}
}
