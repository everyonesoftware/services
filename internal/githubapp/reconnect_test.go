package githubapp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"golang.org/x/crypto/nacl/box"
)

type reconnectClient interface {
	ReconnectConfiguration(context.Context, App, Repository, string, map[string]string) error
}

func TestReconnectConfigurationPreflightsBeforeReplacingRevokedSecret(t *testing.T) {
	app, _ := testApp(t)
	pub, priv, _ := box.GenerateKey(rand.Reader)
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "replace", true: "conflict"}[conflict], func(t *testing.T) {
			variables := map[string]string{enabledName: "false", "TAILSCALE_TAGS": "tag:services"}
			if conflict {
				variables["TAILSCALE_TAGS"] = "tag:other"
			}
			checked := map[string]bool{}
			puts := 0
			c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/secrets/public-key"):
					return response(200, map[string]string{"key_id": "key-1", "key": base64.StdEncoding.EncodeToString(pub[:])}), nil
				case strings.HasSuffix(r.URL.Path, "/secrets/"+secretName):
					if r.Method == "GET" {
						return response(200, map[string]string{"name": secretName}), nil
					}
					if r.Method != "PUT" || conflict || len(checked) != 3 {
						t.Fatal("write before full conflict preflight")
					}
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					encrypted, _ := base64.StdEncoding.DecodeString(body["encrypted_value"])
					plain, ok := box.OpenAnonymous(nil, encrypted, pub, priv)
					if !ok || string(plain) != strings.Repeat("n", 32) {
						t.Fatal("not fresh secret")
					}
					puts++
					return response(204, nil), nil
				case strings.Contains(r.URL.Path, "/variables"):
					if r.Method == "GET" {
						name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
						checked[name] = true
						value, ok := variables[name]
						if !ok {
							return response(404, nil), nil
						}
						return response(200, variable{Name: name, Value: value}), nil
					}
					if r.Method != "POST" || conflict {
						t.Fatal("overwrote variable")
					}
					var v variable
					json.NewDecoder(r.Body).Decode(&v)
					variables[v.Name] = v.Value
					return response(201, nil), nil
				}
				t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
				return nil, nil
			}))
			client, ok := any(c).(reconnectClient)
			if !ok {
				t.Fatal("explicit reconnect configuration is unavailable")
			}
			desired := map[string]string{enabledName: "false", "SERVICES_PUBLISH_URL": "https://services.example/api/images/published", "TAILSCALE_TAGS": "tag:services"}
			err := client.ReconnectConfiguration(context.Background(), app, repository, strings.Repeat("n", 32), desired)
			if conflict {
				if !errors.Is(err, ErrConflict) || puts != 0 {
					t.Fatalf("conflict: %v puts=%d", err, puts)
				}
			} else {
				if err != nil || puts != 1 || variables["SERVICES_PUBLISH_URL"] != desired["SERVICES_PUBLISH_URL"] {
					t.Fatalf("reconnect: %v puts=%d", err, puts)
				}
				if err = client.ReconnectConfiguration(context.Background(), app, repository, strings.Repeat("n", 32), desired); err != nil || puts != 2 {
					t.Fatalf("retry: %v puts=%d", err, puts)
				}
			}
		})
	}
}
