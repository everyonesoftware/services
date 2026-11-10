package auth

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScopedKeyLifecycleAndHashStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key, token, err := s.CreateKey("actions", []string{"publications:write"}, []string{"ghcr.io/acme/app"}, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if key.ID == "" || key.Prefix == "" || len(token) < 43 || key.CreatedAt == "" {
		t.Fatalf("invalid metadata=%#v", key)
	}
	list, err := s.ListKeys()
	if err != nil || len(list) != 1 {
		t.Fatalf("keys=%v %v", list, err)
	}
	serialized, _ := json.Marshal(list)
	if strings.Contains(string(serialized), token) {
		t.Fatal("list leaked token")
	}
	var stored []byte
	if err = s.db.QueryRow("SELECT token_hash FROM api_keys WHERE id=?", key.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256([]byte(token))
	if string(stored) != string(expected[:]) {
		t.Fatal("key not stored as SHA256 digest")
	}
	authenticated, err := s.AuthenticateKey(token)
	if err != nil || authenticated.ID != key.ID || authenticated.LastUsedAt == "" {
		t.Fatalf("authenticate=%#v %v", authenticated, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.AuthenticateKey(token); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeKey(key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateKey(token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked=%v", err)
	}
}
func TestKeyValidationRejectsBroadAndAmbiguousPermissions(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		scopes, repos []string
		expiry        string
	}{
		{nil, nil, ""}, {[]string{"admin"}, nil, ""}, {[]string{"services:write"}, nil, ""},
		{[]string{"publications:write"}, nil, ""}, {[]string{"publications:write"}, []string{"*"}, ""},
		{[]string{"publications:write"}, []string{"ghcr.io/acme/app:latest"}, ""},
		{[]string{"publications:write"}, []string{"GHCR.IO/acme/app"}, ""},
		{[]string{"publications:write"}, []string{"ghcr.io/acme/*"}, ""},
		{[]string{"services:read"}, nil, "not-a-date"},
		{[]string{"services:read"}, nil, time.Now().Add(-time.Hour).Format(time.RFC3339)},
	} {
		if _, _, err = s.CreateKey("invalid", tc.scopes, tc.repos, tc.expiry); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted invalid %#v: %v", tc, err)
		}
	}
	if _, _, err = s.CreateKey("", []string{"services:read"}, nil, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("blank name=%v", err)
	}
	if _, err = s.AuthenticateKey("invalid"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown key=%v", err)
	}
}
func TestExpiredKeysCannotAuthenticate(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key, token, err := s.CreateKey("reader", []string{"services:read"}, nil, time.Now().Add(time.Hour).Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE api_keys SET expires_at=? WHERE id=?", time.Now().Add(-time.Hour).Unix(), key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateKey(token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired=%v", err)
	}
}
