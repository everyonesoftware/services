package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"services/internal/dashboard"
	"services/internal/updates"
)

func TestAuthenticationStartupCreatesPersistentLocalSetupToken(t *testing.T) {
	dir := t.TempDir()
	s := dashboard.New(nil, 1)
	store, err := configureAuthenticationAt(s, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path := filepath.Join(dir, "admin-setup-token")
	token, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(token))) < 32 {
		t.Fatalf("setup token invalid %v", err)
	}
	s2 := dashboard.New(nil, 1)
	store2, err := configureAuthenticationAt(s2, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	again, err := os.ReadFile(path)
	if err != nil || string(again) != string(token) {
		t.Fatal("restart changed bootstrap token")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/api/version", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unprotected startup=%d", w.Code)
	}
	if _, err := store.Bootstrap("admin", "a sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	s3 := dashboard.New(nil, 1)
	store3, err := configureAuthenticationAt(s3, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store3.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale setup token retained: %v", err)
	}
}
func TestAuthenticationStartupRejectsMissingStorageAndMalformedToken(t *testing.T) {
	for _, path := range []string{"", "relative"} {
		if store, err := configureAuthenticationAt(dashboard.New(nil, 1), path); err == nil || store != nil {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "admin-setup-token"), []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if store, err := configureAuthenticationAt(dashboard.New(nil, 1), dir); err == nil || store != nil {
		t.Fatal("accepted invalid setup token")
	}
}

func TestManagedPublicationStoreNeedsNoEnvironmentCredential(t *testing.T) {
	dir := t.TempDir()
	handler := dashboard.New(publicationConfigSource{}, 1)
	authentication, err := configureAuthenticationAt(handler, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer authentication.Close()
	store, err := configurePublicationsAt(handler, updates.NewChecker(nil, 0), dir, "", nil)
	if err != nil || store == nil {
		t.Fatalf("managed receiver: %v", err)
	}
	defer store.Close()
}
