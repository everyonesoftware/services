package githubapp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

func TestConfigurePreflightsAllConflictsBeforeWrites(t *testing.T) {
	app, _ := testApp(t)
	for _, secretExists := range []bool{true, false} {
		writes := 0
		c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
			if r.Method != "GET" {
				writes++
				t.Fatalf("wrote before preflight: %s", r.URL.Path)
			}
			if strings.HasSuffix(r.URL.Path, "/secrets/SERVICES_PUBLISH_TOKEN") {
				if secretExists {
					return response(200, map[string]any{"name": "SERVICES_PUBLISH_TOKEN"}), nil
				}
				return response(404, nil), nil
			}
			if strings.HasSuffix(r.URL.Path, "/TAILSCALE_TAGS") {
				return response(200, map[string]any{"name": "TAILSCALE_TAGS", "value": "other"}), nil
			}
			return response(404, nil), nil
		}))
		e := c.Configure(context.Background(), app, repository, strings.Repeat("s", 32), map[string]string{"SERVICES_PUBLISH_NOTIFICATIONS": "false", "TAILSCALE_TAGS": "tag:services"})
		if !errors.Is(e, ErrConflict) || writes != 0 {
			t.Fatalf("%v writes %d", e, writes)
		}
	}
}
func TestConfigureEncryptsSecretAndVerifiesReadbacks(t *testing.T) {
	app, _ := testApp(t)
	pub, priv, e := box.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	variables := map[string]string{}
	secretPresent := false
	put := false
	gets := map[string]bool{}
	desired := map[string]string{"SERVICES_PUBLISH_NOTIFICATIONS": "false", "SERVICES_PUBLISH_URL": "https://services.example/api/images/published", "TAILSCALE_TAGS": "tag:services"}
	variables["TAILSCALE_TAGS"] = "tag:services"
	c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/secrets/public-key") {
			return response(200, map[string]any{"key_id": "key-1", "key": base64.StdEncoding.EncodeToString(pub[:])}), nil
		}
		if strings.HasSuffix(r.URL.Path, "/secrets/SERVICES_PUBLISH_TOKEN") {
			if r.Method == "GET" {
				if secretPresent {
					return response(200, map[string]any{"name": "SERVICES_PUBLISH_TOKEN"}), nil
				}
				return response(404, nil), nil
			}
			if r.Method != "PUT" {
				t.Fatal("secret method")
			}
			if len(gets) != len(desired) {
				t.Fatal("not all variable conflicts checked")
			}
			var body struct {
				KeyID string `json:"key_id"`
				Value string `json:"encrypted_value"`
			}
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
			cipher, e := base64.StdEncoding.DecodeString(body.Value)
			if e != nil {
				t.Fatal(e)
			}
			plain, ok := box.OpenAnonymous(nil, cipher, pub, priv)
			if !ok || string(plain) != strings.Repeat("s", 32) || body.KeyID != "key-1" {
				t.Fatal("invalid secret encryption")
			}
			secretPresent = true
			put = true
			return response(201, nil), nil
		}
		if strings.Contains(r.URL.Path, "/actions/variables") {
			if r.Method == "GET" {
				name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				gets[name] = true
				v, ok := variables[name]
				if !ok {
					return response(404, nil), nil
				}
				return response(200, map[string]any{"name": name, "value": v}), nil
			}
			if r.Method != "POST" {
				t.Fatal("must not overwrite same variable")
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			variables[body["name"]] = body["value"]
			return response(201, nil), nil
		}
		t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		return nil, nil
	}))
	if e := c.Configure(context.Background(), app, repository, strings.Repeat("s", 32), desired); e != nil {
		t.Fatal(e)
	}
	if !put || len(variables) != len(desired) {
		t.Fatal("missing writes")
	}
}
func TestConfigureRejectsUnsafeInputWithoutRequests(t *testing.T) {
	app, _ := testApp(t)
	c := fakeClient(t, func(*http.Request) (*http.Response, error) { t.Fatal("request on invalid input"); return nil, nil })
	for _, vars := range []map[string]string{{"UNRELATED": "x"}, {"SERVICES_PUBLISH_NOTIFICATIONS": "true"}} {
		if e := c.Configure(context.Background(), app, repository, "secret", vars); e == nil {
			t.Fatal("accepted unsafe config")
		}
	}
}
func TestConfigureDetectsReadbackMismatch(t *testing.T) {
	app, _ := testApp(t)
	pub, _, _ := box.GenerateKey(rand.Reader)
	written := false
	c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/secrets/public-key"):
			return response(200, map[string]any{"key_id": "key-1", "key": base64.StdEncoding.EncodeToString(pub[:])}), nil
		case r.Method == "PUT":
			written = true
			return response(201, nil), nil
		case strings.Contains(r.URL.Path, "/secrets/") && written:
			return response(200, map[string]any{"name": "wrong"}), nil
		default:
			return response(404, nil), nil
		}
	}))
	if e := c.Configure(context.Background(), app, repository, strings.Repeat("s", 32), map[string]string{"SERVICES_PUBLISH_NOTIFICATIONS": "false"}); e == nil {
		t.Fatal("accepted false readback")
	}
}
func TestSetEnabledWritesExactBooleanAndVerifies(t *testing.T) {
	app, _ := testApp(t)
	for _, existing := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			for _, mismatch := range []bool{false, true} {
				written := false
				want := "false"
				if enabled {
					want = "true"
				}
				c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
					if r.Method == "GET" {
						if written {
							value := want
							if mismatch {
								value = "wrong"
							}
							return response(200, map[string]any{"name": "SERVICES_PUBLISH_NOTIFICATIONS", "value": value}), nil
						}
						if !existing {
							return response(404, nil), nil
						}
						return response(200, map[string]any{"name": "SERVICES_PUBLISH_NOTIFICATIONS", "value": "old"}), nil
					}
					method := "POST"
					if existing {
						method = "PATCH"
					}
					if r.Method != method {
						t.Fatalf("method %s", r.Method)
					}
					b, _ := io.ReadAll(r.Body)
					var payload map[string]string
					json.Unmarshal(b, &payload)
					if payload["name"] != "SERVICES_PUBLISH_NOTIFICATIONS" || payload["value"] != want {
						t.Fatalf("payload %s", b)
					}
					written = true
					if existing {
						return response(204, nil), nil
					}
					return response(201, nil), nil
				}))
				e := c.SetEnabled(context.Background(), app, repository, enabled)
				if (e != nil) != mismatch || !written {
					t.Fatalf("%v written=%v", e, written)
				}
			}
		}
	}
}
