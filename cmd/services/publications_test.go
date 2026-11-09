package main

import (
	"path/filepath"
	"strings"
	"testing"

	"services/internal/dashboard"
	"services/internal/groups"
	"services/internal/updates"
)

type publicationConfigSource struct{}

func (publicationConfigSource) Containers() ([]groups.Container, error) { return nil, nil }

func TestConfigurePublicationStoreAttachesPersistentReceiver(t *testing.T) {
	handler := dashboard.New(publicationConfigSource{}, 1)
	checker := updates.NewChecker(nil, 0)
	dir := t.TempDir()
	store, err := configurePublicationsAt(handler, checker, dir, "test-notification-token-with-at-least-32-characters", []string{"ghcr.io/acme/app"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reopened, err := updates.OpenPublicationStore(filepath.Join(dir, "publications.db"))
	if err != nil {
		t.Fatal(err)
	}
	reopened.Close()
}

func TestConfigurePublicationsDisabledUnlessFullyConfigured(t *testing.T) {
	for _, tc := range []struct {
		token, repos, data string
		wantError          bool
	}{
		{"", "", "", false}, {"test-notification-token-with-at-least-32-characters", "", "/data", true},
		{"", "ghcr.io/acme/app", "/data", true}, {"test-notification-token-with-at-least-32-characters", "ghcr.io/acme/app", "", true},
	} {
		t.Setenv("SERVICES_PUBLISH_TOKEN", tc.token)
		t.Setenv("SERVICES_PUBLISH_REPOSITORIES", tc.repos)
		t.Setenv("SERVICES_DATA_DIR", tc.data)
		handler := dashboard.New(publicationConfigSource{}, 1)
		checker := updates.NewChecker(nil, 0)
		store, err := configurePublications(handler, checker)
		if (err != nil) != tc.wantError || store != nil {
			t.Fatalf("configuration err=%v store=%v expected error=%v", err, store, tc.wantError)
		}
	}
}

func TestInvalidPublicationConfigDoesNotReturnOpenStore(t *testing.T) {
	handler := dashboard.New(publicationConfigSource{}, 1)
	checker := updates.NewChecker(nil, 0)
	store, err := configurePublicationsAt(handler, checker, t.TempDir(), "short", []string{"ghcr.io/acme/app"})
	if err == nil || store != nil {
		t.Fatal("invalid config accepted")
	}
}

func TestPublicationsRequireVerifiedPersistentMount(t *testing.T) {
	t.Setenv("SERVICES_PUBLISH_TOKEN", "test-notification-token-with-at-least-32-characters")
	t.Setenv("SERVICES_PUBLISH_REPOSITORIES", "ghcr.io/acme/app")
	t.Setenv("SERVICES_DATA_DIR", "/data")
	t.Setenv("DOCKER_SOCKET", filepath.Join(t.TempDir(), "absent.sock"))
	handler := dashboard.New(publicationConfigSource{}, 1)
	store, err := configurePublications(handler, updates.NewChecker(nil, 0))
	if store != nil {
		store.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "verify publication storage") {
		t.Fatalf("expected mount verification failure, got %v", err)
	}
}
