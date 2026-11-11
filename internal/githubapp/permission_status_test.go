package githubapp

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPermissionChecksFailuresAreSanitized(t *testing.T) {
	app, _ := testApp(t)
	for _, operation := range []string{"app", "mint", "readback"} {
		for _, failure := range []string{"401", "403", "422", "500", "redirect", "network", "cancel", "malformed", "oversized"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				calls := 0
				c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
					calls++
					if operation == "readback" && calls == 1 {
						return response(201, map[string]any{"token": "scoped-secret", "expires_at": time.Now().Add(time.Hour)}), nil
					}
					statusOK := 200
					if operation == "mint" {
						statusOK = 201
					}
					switch failure {
					case "network":
						return nil, errors.New("transport-secret https://secret.example")
					case "cancel":
						cancel()
						return nil, r.Context().Err()
					case "redirect":
						resp := response(302, map[string]string{"message": "provider-secret"})
						resp.Header.Set("Location", "https://secret.example")
						return resp, nil
					case "malformed":
						return &http.Response{StatusCode: statusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("provider-secret"))}, nil
					case "oversized":
						return &http.Response{StatusCode: statusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("s", maxBody+1)))}, nil
					default:
						status := map[string]int{"401": 401, "403": 403, "422": 422, "500": 500}[failure]
						return response(status, map[string]string{"message": "provider-secret", "token": "scoped-secret", "pem": app.PEM}), nil
					}
				})
				var err error
				if operation == "app" {
					var got PermissionStatus
					got, err = c.CheckPermissions(ctx, app)
					if got != (PermissionStatus{}) {
						t.Fatalf("partial status leaked: %+v", got)
					}
				} else {
					err = c.VerifyRepositoryPermissions(ctx, app, repository)
				}
				if err == nil {
					t.Fatal("failure accepted")
				}
				text := err.Error()
				for _, secret := range []string{"provider-secret", "transport-secret", "scoped-secret", "PRIVATE KEY", "secret.example"} {
					if strings.Contains(text, secret) {
						t.Fatalf("leaked secret: %v", err)
					}
				}
				expectedCalls := 1
				if operation == "readback" {
					expectedCalls = 2
				}
				if calls != expectedCalls {
					t.Fatalf("unexpected requests %d", calls)
				}
				if operation != "app" {
					for _, hint := range []string{"Variables", "Read & write", "approve", "installation"} {
						if !strings.Contains(text, hint) {
							t.Errorf("missing actionable hint %q: %v", hint, err)
						}
					}
					if strings.Contains(text, "missing permission") || strings.Contains(text, "permission denied") {
						t.Fatalf("unproven missing permission claim: %v", err)
					}
					if failure == "cancel" && !errors.Is(err, context.Canceled) {
						t.Errorf("cancellation not preserved: %v", err)
					}
				}
			})
		}
	}
}

func TestVerifyRepositoryPermissionsRejectsChangedRepository(t *testing.T) {
	app, _ := testApp(t)
	for _, field := range []string{"id", "full_name", "default_branch"} {
		t.Run(field, func(t *testing.T) {
			calls := 0
			c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return response(201, map[string]any{"token": "scoped-secret", "expires_at": time.Now().Add(time.Hour)}), nil
				}
				got := map[string]any{"id": repository.ID, "full_name": repository.FullName, "default_branch": repository.DefaultBranch}
				if field == "id" {
					got[field] = repository.ID + 1
				} else {
					got[field] = "other"
				}
				return response(200, got), nil
			})
			if err := c.VerifyRepositoryPermissions(context.Background(), app, repository); err == nil || !strings.Contains(err.Error(), "identity changed") {
				t.Fatalf("identity mismatch: %v", err)
			}
			if calls != 2 {
				t.Fatalf("unexpected requests %d", calls)
			}
		})
	}
}

func TestVerifyRepositoryPermissionsUsesFreshScopedTokenAndReadback(t *testing.T) {
	app, _ := testApp(t)
	calls := 0
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if calls%2 == 1 {
			if r.Method != "POST" || r.URL.RequestURI() != fmt.Sprintf("/app/installations/%d/access_tokens", repository.InstallationID) {
				t.Fatalf("unexpected token request %s %s", r.Method, r.URL)
			}
			if len(strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")) != 3 {
				t.Fatal("token mint must use JWT")
			}
			var input struct {
				Permissions   map[string]string `json:"permissions"`
				RepositoryIDs []int64           `json:"repository_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(input.Permissions, map[string]string{"metadata": "read", "actions_variables": "write"}) || !reflect.DeepEqual(input.RepositoryIDs, []int64{repository.ID}) {
				t.Fatalf("wrong scope %+v", input)
			}
			return response(201, map[string]any{"token": fmt.Sprintf("scoped-secret-%d", calls), "expires_at": time.Now().Add(time.Hour)}), nil
		}
		if r.Method != "GET" || r.URL.RequestURI() != "/repos/"+repository.FullName || r.Header.Get("Authorization") != fmt.Sprintf("Bearer scoped-secret-%d", calls-1) {
			t.Fatalf("unexpected readback %s %s", r.Method, r.URL)
		}
		return response(200, map[string]any{"id": repository.ID, "full_name": repository.FullName, "default_branch": repository.DefaultBranch}), nil
	})
	for i := 0; i < 2; i++ {
		if err := c.VerifyRepositoryPermissions(context.Background(), app, repository); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 4 {
		t.Fatalf("wanted a fresh mint and readback each time, calls=%d", calls)
	}
}

func TestPermissionStatusOwnerAndGrantValidation(t *testing.T) {
	app, _ := testApp(t)
	for _, tc := range []struct {
		name, owner, kind, grant, url string
		invalid                       bool
	}{
		{name: "user missing", owner: "a", kind: "User", url: "https://github.com/settings/apps/services/permissions"},
		{name: "user read", owner: "Alice-9", kind: "User", grant: "read", url: "https://github.com/settings/apps/services/permissions"},
		{name: "org write", owner: "Example-Org", kind: "Organization", grant: "write", url: "https://github.com/organizations/Example-Org/settings/apps/services/permissions"},
		{name: "max owner", owner: strings.Repeat("a", 39), kind: "Organization", grant: "WRITE", url: "https://github.com/organizations/" + strings.Repeat("a", 39) + "/settings/apps/services/permissions"},
		{name: "missing owner", kind: "User", invalid: true},
		{name: "long owner", owner: strings.Repeat("a", 40), kind: "User", invalid: true},
		{name: "leading dash", owner: "-a", kind: "User", invalid: true},
		{name: "trailing dash", owner: "a-", kind: "User", invalid: true},
		{name: "double dash", owner: "a--b", kind: "Organization", invalid: true},
		{name: "underscore", owner: "a_b", kind: "User", invalid: true},
		{name: "unicode", owner: "é", kind: "User", invalid: true},
		{name: "path injection", owner: "evil/../settings", kind: "Organization", invalid: true},
		{name: "space", owner: " alice", kind: "User", invalid: true},
		{name: "missing type", owner: "alice", invalid: true},
		{name: "bot", owner: "alice", kind: "Bot", invalid: true},
		{name: "type casing", owner: "alice", kind: "user", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			permissions := map[string]string{}
			if tc.grant != "" {
				permissions["actions_variables"] = tc.grant
			}
			c := fakeClient(t, func(*http.Request) (*http.Response, error) {
				return response(200, map[string]any{"id": app.ID, "slug": app.Slug, "owner": map[string]string{"login": tc.owner, "type": tc.kind}, "permissions": permissions}), nil
			})
			got, err := c.CheckPermissions(context.Background(), app)
			if tc.invalid {
				if err == nil || got != (PermissionStatus{}) {
					t.Fatalf("accepted invalid owner: %+v %v", got, err)
				}
				return
			}
			if err != nil || got.SettingsURL != tc.url || got.VariablesWrite != (tc.grant == "write") {
				t.Fatalf("got %+v %v", got, err)
			}
		})
	}
}

func TestPermissionStatusRejectsInvalidIdentity(t *testing.T) {
	app, _ := testApp(t)
	for _, payload := range []map[string]any{
		{"id": app.ID + 1, "slug": app.Slug}, {"id": app.ID, "slug": "Services"},
		{"id": app.ID}, {"slug": app.Slug}, {},
		{"id": app.ID, "slug": "../secret"},
	} {
		payload["owner"] = map[string]string{"login": "alice", "type": "User"}
		c := fakeClient(t, func(*http.Request) (*http.Response, error) { return response(200, payload), nil })
		got, err := c.CheckPermissions(context.Background(), app)
		if err == nil || got != (PermissionStatus{}) {
			t.Fatalf("accepted identity %+v: %+v %v", payload, got, err)
		}
	}
}

func TestPermissionStatusMissingPermissionsIsNormal(t *testing.T) {
	app, _ := testApp(t)
	for _, explicitNull := range []bool{false, true} {
		c := fakeClient(t, func(*http.Request) (*http.Response, error) {
			payload := map[string]any{"id": app.ID, "slug": app.Slug, "owner": map[string]string{"login": "alice", "type": "User"}}
			if explicitNull {
				payload["permissions"] = nil
			}
			return response(200, payload), nil
		})
		got, err := c.CheckPermissions(context.Background(), app)
		if err != nil || got.VariablesWrite || got.SettingsURL != "https://github.com/settings/apps/services/permissions" {
			t.Fatalf("missing permissions must be normal false: %+v %v", got, err)
		}
	}
}

func TestPermissionStatusMissingOwnerIsRejected(t *testing.T) {
	app, _ := testApp(t)
	for _, explicitNull := range []bool{false, true} {
		c := fakeClient(t, func(*http.Request) (*http.Response, error) {
			payload := map[string]any{"id": app.ID, "slug": app.Slug, "permissions": map[string]string{"actions_variables": "write"}}
			if explicitNull {
				payload["owner"] = nil
			}
			return response(200, payload), nil
		})
		got, err := c.CheckPermissions(context.Background(), app)
		if err == nil || got != (PermissionStatus{}) {
			t.Fatalf("missing owner must not produce settings URL: %+v %v", got, err)
		}
	}
}

func TestPermissionChecksRejectInvalidLocalCredentialsWithoutRequests(t *testing.T) {
	app, _ := testApp(t)
	for _, field := range []string{"id", "slug", "pem"} {
		t.Run(field, func(t *testing.T) {
			bad := app
			switch field {
			case "id":
				bad.ID = 0
			case "slug":
				bad.Slug = "../credential-secret"
			case "pem":
				bad.PEM = "credential-secret"
			}
			c := fakeClient(t, func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid credentials reached GitHub")
				return nil, nil
			})
			if got, err := c.CheckPermissions(context.Background(), bad); err == nil || got != (PermissionStatus{}) || strings.Contains(err.Error(), "credential-secret") {
				t.Fatalf("invalid app accepted or leaked: %+v %v", got, err)
			}
			if err := c.VerifyRepositoryPermissions(context.Background(), bad, repository); err == nil || strings.Contains(err.Error(), "credential-secret") {
				t.Fatalf("invalid app accepted or leaked: %v", err)
			}
		})
	}
}

func TestPermissionStatusChecksAppWithJWT(t *testing.T) {
	app, key := testApp(t)
	calls := 0
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.RequestURI() != "/app" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
		parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
		if len(parts) != 3 {
			t.Fatal("missing JWT")
		}
		payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims struct {
			Iss      int64
			Iat, Exp int64
		}
		if err := json.Unmarshal(payload, &claims); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Unix()
		if claims.Iss != app.ID || claims.Iat > now || claims.Exp <= now || claims.Exp > now+600 {
			t.Fatalf("invalid JWT claims %s", payload)
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
			t.Fatal(err)
		}
		return response(200, map[string]any{"id": app.ID, "slug": app.Slug, "owner": map[string]string{"login": "alice", "type": "User"}, "permissions": map[string]string{"actions_variables": "write"}}), nil
	})
	got, err := c.CheckPermissions(context.Background(), app)
	want := PermissionStatus{VariablesWrite: true, SettingsURL: "https://github.com/settings/apps/services/permissions"}
	if err != nil || got != want || calls != 1 {
		t.Fatalf("got %+v err=%v calls=%d", got, err, calls)
	}
	b, _ := json.Marshal(got)
	if string(b) != `{"variablesWrite":true,"settingsUrl":"https://github.com/settings/apps/services/permissions"}` {
		t.Fatalf("JSON contract: %s", b)
	}
}
