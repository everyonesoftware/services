package githubapp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/nacl/box"
	"net/http"
	"strings"
	"testing"
)

func TestRecoverConfigurationPreservesExistingSecretAndReconcilesOnlyMissingVariables(t *testing.T) {
	app, _ := testApp(t)
	vars := map[string]string{"TAILSCALE_TAGS": "tag:services"}
	writes := 0
	secretReads := 0
	c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "/secrets/") {
			if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/"+secretName) {
				t.Fatal("recovery must not replace or inspect existing secret")
			}
			secretReads++
			return response(200, map[string]string{"name": secretName}), nil
		}
		if r.Method == "GET" {
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			v, ok := vars[name]
			if !ok {
				return response(404, nil), nil
			}
			return response(200, map[string]string{"name": name, "value": v}), nil
		}
		writes++
		if r.Method != "POST" {
			t.Fatal("overwrote variable")
		}
		var v variable
		json.NewDecoder(r.Body).Decode(&v)
		vars[v.Name] = v.Value
		return response(201, nil), nil
	}))
	desired := map[string]string{enabledName: "false", "TAILSCALE_TAGS": "tag:services"}
	if err := c.RecoverConfiguration(context.Background(), app, repository, strings.Repeat("s", 32), desired); err != nil || writes != 1 || secretReads == 0 {
		t.Fatalf("%v writes=%d reads=%d", err, writes, secretReads)
	}
	if err := c.RecoverConfiguration(context.Background(), app, repository, strings.Repeat("s", 32), desired); err != nil || writes != 1 {
		t.Fatal("recovery was not idempotent", err)
	}
	vars["TAILSCALE_TAGS"] = "other"
	if err := c.RecoverConfiguration(context.Background(), app, repository, strings.Repeat("s", 32), desired); !errors.Is(err, ErrConflict) || writes != 1 {
		t.Fatal("conflicting variables overwritten", err)
	}
}

func TestRecoverConfigurationUploadsSavedKeyWhenAbsentAndReadsMetadata(t *testing.T) {
	app, _ := testApp(t)
	pub, priv, _ := box.GenerateKey(rand.Reader)
	present := false
	puts := 0
	readback := false
	c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/public-key") {
			return response(200, map[string]string{"key_id": "key-1", "key": base64.StdEncoding.EncodeToString(pub[:])}), nil
		}
		if strings.HasSuffix(r.URL.Path, "/"+secretName) {
			if r.Method == "GET" {
				if present {
					readback = true
					return response(200, map[string]string{"name": secretName}), nil
				}
				return response(404, nil), nil
			}
			if r.Method != "PUT" || present {
				t.Fatal("unsafe recovery write")
			}
			var v map[string]string
			json.NewDecoder(r.Body).Decode(&v)
			encrypted, _ := base64.StdEncoding.DecodeString(v["encrypted_value"])
			plain, ok := box.OpenAnonymous(nil, encrypted, pub, priv)
			if !ok || string(plain) != strings.Repeat("s", 32) {
				t.Fatal("not saved key")
			}
			puts++
			present = true
			return response(201, nil), nil
		}
		return response(200, map[string]string{"name": enabledName, "value": "false"}), nil
	}))
	if err := c.RecoverConfiguration(context.Background(), app, repository, strings.Repeat("s", 32), map[string]string{enabledName: "false"}); err != nil || puts != 1 || !readback {
		t.Fatalf("%v puts=%d readback=%v", err, puts, readback)
	}
}

func TestReplaceSecretEncryptsExplicitRotationAndVerifiesMetadata(t *testing.T) {
	app, _ := testApp(t)
	pub, priv, _ := box.GenerateKey(rand.Reader)
	for _, status := range []int{201, 204} {
		for _, mismatch := range []bool{false, true} {
			put := false
			readback := false
			c := fakeClient(t, repositoryTransport(t, func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "/public-key") {
					return response(200, map[string]string{"key_id": "key-1", "key": base64.StdEncoding.EncodeToString(pub[:])}), nil
				}
				if r.Method == "PUT" {
					var v map[string]string
					json.NewDecoder(r.Body).Decode(&v)
					cipher, _ := base64.StdEncoding.DecodeString(v["encrypted_value"])
					plain, ok := box.OpenAnonymous(nil, cipher, pub, priv)
					if !ok || string(plain) != strings.Repeat("n", 32) {
						t.Fatal("invalid rotated encryption")
					}
					put = true
					return response(status, nil), nil
				}
				if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/"+secretName) && put {
					readback = true
					if mismatch {
						return response(404, nil), nil
					}
					return response(200, map[string]string{"name": secretName}), nil
				}
				t.Fatalf("unexpected rotation request %s %s", r.Method, r.URL)
				return nil, nil
			}))
			err := c.ReplaceSecret(context.Background(), app, repository, strings.Repeat("n", 32))
			if (err != nil) != mismatch || !put || !readback {
				t.Fatalf("%v put=%v readback=%v", err, put, readback)
			}
		}
	}
}

func TestRecoveryAndRotationRejectUnsafeInputBeforeRequests(t *testing.T) {
	app, _ := testApp(t)
	c := fakeClient(t, func(*http.Request) (*http.Response, error) { t.Fatal("unsafe input made request"); return nil, nil })
	if err := c.ReplaceSecret(context.Background(), app, repository, "short"); err == nil {
		t.Fatal("short secret accepted")
	}
	for _, vars := range []map[string]string{{enabledName: "true"}, {enabledName: "false", "unrelated": "bad"}} {
		if err := c.RecoverConfiguration(context.Background(), app, repository, strings.Repeat("s", 32), vars); err == nil {
			t.Fatal("unsafe variables accepted")
		}
	}
}
