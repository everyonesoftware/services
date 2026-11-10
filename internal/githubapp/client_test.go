package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}
}
func testApp(t *testing.T) (App, *rsa.PrivateKey) {
	t.Helper()
	k, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	return App{ID: 42, Slug: "services", Name: "Services", PEM: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))}, k
}
func fakeClient(t *testing.T, f transportFunc) *Client {
	t.Helper()
	return NewClient(&http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.github.com" {
			t.Fatalf("unsafe host: %s", r.URL)
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Fatal("missing request deadline")
		}
		return f(r)
	})})
}

func TestExchangeManifestValidatesCredentials(t *testing.T) {
	app, _ := testApp(t)
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/app-manifests/abc/conversions" {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		return response(201, map[string]any{"id": app.ID, "slug": app.Slug, "name": app.Name, "pem": app.PEM, "client_secret": "never-return"}), nil
	})
	got, e := c.ExchangeManifest(context.Background(), "abc")
	if e != nil || got != app {
		t.Fatalf("got %#v, %v", got, e)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "PRIVATE") || strings.Contains(string(b), "never-return") {
		t.Fatal("credential serialization")
	}
	for _, bad := range []map[string]any{{"id": 0, "slug": "ok", "pem": app.PEM}, {"id": 1, "slug": "../bad", "pem": app.PEM}, {"id": 1, "slug": "ok", "pem": "sensitive-malformed-key"}} {
		c = fakeClient(t, func(*http.Request) (*http.Response, error) { return response(201, bad), nil })
		if _, e = c.ExchangeManifest(context.Background(), "abc"); e == nil || strings.Contains(e.Error(), "sensitive") {
			t.Fatalf("invalid credentials: %v", e)
		}
	}
}
func TestRepositoriesAuthenticatesJWT(t *testing.T) {
	app, key := testApp(t)
	calls := 0
	c := fakeClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
		if len(parts) != 3 {
			t.Fatal("not JWT")
		}
		h, _ := base64.RawURLEncoding.DecodeString(parts[0])
		if !strings.Contains(string(h), "RS256") {
			t.Fatal("algorithm")
		}
		p, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims struct {
			Iss      int64 `json:"iss"`
			Iat, Exp int64
		}
		if e := json.Unmarshal(p, &claims); e != nil {
			t.Fatal(e)
		}
		now := time.Now().Unix()
		if claims.Iss != app.ID || claims.Iat > now || claims.Exp <= now || claims.Exp > now+600 {
			t.Fatalf("claims: %s", p)
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if e := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); e != nil {
			t.Fatal(e)
		}
		return response(200, []any{}), nil
	})
	repos, e := c.Repositories(context.Background(), app)
	if e != nil || len(repos) != 0 || calls != 1 {
		t.Fatalf("repos %v, %v", repos, e)
	}
}
func TestTransportFailureIsSanitizedAndRedirectNotFollowed(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		calls := 0
		c := fakeClient(t, func(*http.Request) (*http.Response, error) {
			calls++
			if !redirect {
				return nil, fmt.Errorf("token-sensitive-body")
			}
			r := response(302, nil)
			r.Header.Set("Location", "https://evil.example/steal")
			return r, nil
		})
		_, e := c.ExchangeManifest(context.Background(), "abc")
		if e == nil || strings.Contains(e.Error(), "sensitive") || strings.Contains(e.Error(), "evil") || calls != 1 {
			t.Fatalf("unsafe error or redirect: %v calls %d", e, calls)
		}
	}
}
