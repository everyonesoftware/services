package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"services/internal/githubapp"
	"services/internal/publishers"
)

type permissionPublisherFake struct {
	*publisherProviderFake
	grant                    bool
	checkErr                 error
	repositoryErr            error
	checks, repositoryChecks int
}

func (f *permissionPublisherFake) CheckPermissions(context.Context, githubapp.App) (githubapp.PermissionStatus, error) {
	f.checks++
	return githubapp.PermissionStatus{VariablesWrite: f.grant, SettingsURL: "https://github.com/organizations/acme/settings/apps/services-test/permissions"}, f.checkErr
}
func (f *permissionPublisherFake) VerifyRepositoryPermissions(context.Context, githubapp.App, githubapp.Repository) error {
	f.repositoryChecks++
	return f.repositoryErr
}

func TestPublisherManifestOmitsUnsupportedVariablesPermission(t *testing.T) {
	for _, organization := range []string{"", "acme"} {
		t.Run("organization="+organization, func(t *testing.T) {
			s, _, store, _, cookie := publisherServer(t)
			state, _ := store.Load()
			state.App = publishers.App{}
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			w := publisherRequest(s, "POST", "/api/publishers/github/start", map[string]string{"organization": organization}, cookie, "")
			if w.Code != http.StatusOK {
				t.Fatal(w.Code, w.Body.String())
			}
			var start struct{ Manifest string }
			if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil {
				t.Fatal(err)
			}
			var manifest struct {
				Permissions map[string]string `json:"default_permissions"`
			}
			if err := json.Unmarshal([]byte(start.Manifest), &manifest); err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{"actions": "write", "contents": "write", "workflows": "write", "pull_requests": "write", "secrets": "write", "metadata": "read"}
			if len(manifest.Permissions) != len(expected) {
				t.Fatalf("manifest contains unsupported permissions: %v", manifest.Permissions)
			}
			for name, level := range expected {
				if manifest.Permissions[name] != level {
					t.Fatalf("permission %s=%q", name, manifest.Permissions[name])
				}
			}
		})
	}
}

func TestPublisherPermissionCheckPersistsGrantWithoutPolling(t *testing.T) {
	s, _, store, base, cookie := publisherServer(t)
	provider := &permissionPublisherFake{publisherProviderFake: base}
	s.publishers.provider = provider
	for _, grant := range []bool{false, true, false} {
		provider.grant = grant
		w := publisherRequest(s, "POST", "/api/publishers/github/permissions", map[string]any{}, cookie, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var status struct {
			GitHub struct {
				Connected, PermissionsChecked, VariablesWrite bool
				SettingsURL                                   string
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if !status.GitHub.Connected || !status.GitHub.PermissionsChecked || status.GitHub.VariablesWrite != grant || !strings.Contains(status.GitHub.SettingsURL, "/organizations/acme/") {
			t.Fatalf("incorrect permission state: %s", w.Body.String())
		}
		before := provider.checks
		for range 2 {
			w = publisherRequest(s, "GET", "/api/publishers", nil, cookie, "")
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
		}
		if provider.checks != before {
			t.Fatal("local status reads polled GitHub")
		}
		saved, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if !saved.App.PermissionsChecked || saved.App.VariablesWrite != grant || saved.App.SettingsURL != status.GitHub.SettingsURL {
			t.Fatal("permission check was not persisted")
		}
	}
	provider.checkErr = errors.New("GitHub unavailable")
	w := publisherRequest(s, "POST", "/api/publishers/github/permissions", map[string]any{}, cookie, "")
	if w.Code != 502 {
		t.Fatal("provider failure must not claim verification", w.Code)
	}
	saved, _ := store.Load()
	if saved.App.PermissionsChecked || saved.App.VariablesWrite {
		t.Fatal("failed refresh retained a stale verified grant")
	}
}

func TestPublisherPermissionPreflightPreventsSetupWrites(t *testing.T) {
	for _, mode := range []string{"missing-app-grant", "provider-unavailable", "installation-approval-pending"} {
		t.Run(mode, func(t *testing.T) {
			s, keys, store, base, cookie := publisherServer(t)
			provider := &permissionPublisherFake{publisherProviderFake: base, grant: mode != "missing-app-grant"}
			if mode == "provider-unavailable" {
				provider.checkErr = errors.New("GitHub unavailable")
			}
			if mode == "installation-approval-pending" {
				provider.repositoryErr = errors.New("Approve updated Variables permission for this installation")
			}
			s.publishers.provider = provider
			state, _ := store.Load()
			state.App.PermissionsChecked, state.App.VariablesWrite = true, true
			if err := store.Save(state); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/publishers/plan", "/api/publishers", "/api/publishers/workflow"} {
				body := publisherSetupBody()
				if path == "/api/publishers/workflow" {
					delete(body, "approval")
					body["fingerprint"] = strings.Repeat("a", 64)
				}
				w := publisherRequest(s, "POST", path, body, cookie, "")
				if w.Code != 403 {
					t.Fatalf("%s bypassed permission preflight: %d %s", path, w.Code, w.Body.String())
				}
			}
			if base.configured != 0 || base.dispatched != 0 {
				t.Fatal("unverified permission reached remote writes")
			}
			all, err := keys.ListKeys()
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 0 {
				t.Fatal("unverified permission created a publisher key")
			}
			saved, _ := store.Load()
			if len(saved.Connections) != 0 {
				t.Fatal("unverified permission persisted a new connection")
			}
			if provider.checks != 3 {
				t.Fatal("preflight was not fresh for each request")
			}
			if mode == "installation-approval-pending" && provider.repositoryChecks != 3 {
				t.Fatal("installation permission was not verified")
			}
		})
	}
}

func TestPublisherRegistrationPreservesCredentialsWhenPermissionCheckFails(t *testing.T) {
	s, _, store, base, cookie := publisherServer(t)
	provider := &permissionPublisherFake{publisherProviderFake: base, checkErr: errors.New("provider unavailable")}
	s.publishers.provider = provider
	state, _ := store.Load()
	state.App = publishers.App{}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	w := publisherRequest(s, "POST", "/api/publishers/github/start", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var start struct{ Action string }
	if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil {
		t.Fatal(err)
	}
	action, err := url.Parse(start.Action)
	if err != nil {
		t.Fatal(err)
	}
	w = publisherRequest(s, "POST", "/api/publishers/github/complete", map[string]string{"code": strings.Repeat("a", 16), "state": action.Query().Get("state")}, cookie, "")
	if w.Code != 200 {
		t.Fatal("registered App must remain recoverable", w.Code, w.Body.String())
	}
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.App.ID != 1 || state.App.PEM != "private-app-key" || state.App.PermissionsChecked || state.App.VariablesWrite || state.Pending.Hash != "" {
		t.Fatal("permission failure lost registration credentials or claimed verification")
	}
	provider.checkErr, provider.grant = nil, true
	w = publisherRequest(s, "POST", "/api/publishers/github/permissions", map[string]any{}, cookie, "")
	if w.Code != 200 {
		t.Fatal("permission check could not resume without re-registration", w.Code)
	}
	state, _ = store.Load()
	if !state.App.PermissionsChecked || !state.App.VariablesWrite {
		t.Fatal("recovered grant was not persisted")
	}
}

func TestPublisherPermissionCheckRequiresAdministrator(t *testing.T) {
	s, keys, _, base, cookie := publisherServer(t)
	provider := &permissionPublisherFake{publisherProviderFake: base}
	s.publishers.provider = provider
	_, token, err := keys.CreateKey("reader", []string{"services:read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", token} {
		w := publisherRequest(s, "POST", "/api/publishers/github/permissions", map[string]any{}, nil, token)
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("permission check did not require admin", w.Code)
		}
	}
	r := httptest.NewRequest("POST", "https://localhost/api/publishers/github/permissions", strings.NewReader(`{}`))
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-origin permission check accepted", w.Code)
	}
	if provider.checks != 0 {
		t.Fatal("unauthorized request reached GitHub")
	}
}
