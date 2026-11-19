package updates

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestPublicationBindingImmutableOwnershipAndCAS(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	image := publicationFixture().Repository
	absent := PublicationBinding{ImageRepository: image}
	if err := s.ReconcilePublicationRepository(ctx, absent, 42, 7, "acme/app"); err != nil {
		t.Fatal(err)
	}
	first, _ := s.PublicationRepositoryBinding(ctx, image)
	if err := s.ReconcilePublicationRepository(ctx, first, 42, 7, "acme/renamed"); err != nil {
		t.Fatal(err)
	}
	second, _ := s.PublicationRepositoryBinding(ctx, image)
	if second.Revision != 2 || second.SourceRepository != "acme/renamed" {
		t.Fatalf("rename: %+v", second)
	}
	for _, tc := range []struct {
		expected      PublicationBinding
		repo, install int64
		source        string
	}{
		{first, 42, 7, "acme/stale"},
		{second, 43, 7, "acme/replacement"},
		{second, 42, 8, "acme/replacement"},
		{second, 0, 7, "acme/replacement"},
	} {
		if err := s.ReconcilePublicationRepository(ctx, tc.expected, tc.repo, tc.install, tc.source); !errors.Is(err, ErrPublicationConflict) {
			t.Fatalf("must reject %+v: %v", tc, err)
		}
		if got, _ := s.PublicationRepositoryBinding(ctx, image); got != second {
			t.Fatalf("conflict changed binding: %+v", got)
		}
	}
	if err := s.ReconcilePublicationRepository(ctx, second, 42, 7, "acme/third"); err != nil {
		t.Fatal(err)
	}
	third, _ := s.PublicationRepositoryBinding(ctx, image)
	if third.Revision != 3 || third.RepositoryID != 42 || third.InstallationID != 7 || third.SourceRepository != "acme/third" {
		t.Fatalf("second rename: %+v", third)
	}
}

func TestPublicationBindingValidationCapacityAndSchema(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		image, source string
		repo, install int64
	}{
		{"ghcr.io/acme/app:latest", "acme/app", 42, 7},
		{"ghcr.io/Acme/app", "acme/app", 42, 7},
		{"ghcr.io/acme/app", "acme/..", 42, 7},
		{"ghcr.io/acme/app", "bad", 42, 7},
		{"ghcr.io/acme/app", "acme/app", 0, 7},
		{"ghcr.io/acme/app", "acme/app", 42, 0},
		{"ghcr.io/acme/app", "acme/app", -1, 7},
	} {
		s := openPublicationTestStore(t)
		if err := s.ReconcilePublicationRepository(ctx, PublicationBinding{ImageRepository: tc.image}, tc.repo, tc.install, tc.source); err == nil {
			t.Fatalf("invalid binding accepted: %+v", tc)
		}
	}
	s := openPublicationTestStore(t)
	for i := 0; i < maxPublicationRepositories; i++ {
		if _, err := s.db.Exec("INSERT INTO publication_repositories(repository,source) VALUES(?,?)", fmt.Sprintf("ghcr.io/acme/app%d", i), "acme/app"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReconcilePublicationRepository(ctx, PublicationBinding{ImageRepository: "ghcr.io/acme/extra"}, 42, 7, "acme/app"); err == nil {
		t.Fatal("reservation bypassed capacity")
	}
	p := publicationFixture()
	p.Repository = "ghcr.io/acme/app0"
	legacy, _ := s.PublicationRepositoryBinding(ctx, p.Repository)
	if err := s.ReconcilePublicationRepository(ctx, legacy, 42, 7, "acme/app"); err != nil {
		t.Fatalf("existing upgrade at capacity: %v", err)
	}
	if _, err := s.accept(ctx, publicationFixture()); err == nil {
		t.Fatal("accept bypassed reservations capacity")
	}
	if _, err := s.db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePublicationRepository(ctx, legacy, 42, 7, "acme/app"); err == nil {
		t.Fatal("reconcile used future schema")
	}
}

func TestCheckerPublicationBindingForwarding(t *testing.T) {
	ctx := context.Background()
	c := &Checker{}
	expected := PublicationBinding{ImageRepository: publicationFixture().Repository}
	if _, err := c.PublicationRepositoryBinding(ctx, expected.ImageRepository); err == nil {
		t.Fatal("missing store read succeeded")
	}
	if err := c.ReconcilePublicationRepository(ctx, expected, 42, 7, "acme/app"); err == nil {
		t.Fatal("missing store write succeeded")
	}
	s := openPublicationTestStore(t)
	c.SetPublicationStore(s)
	got, err := c.PublicationRepositoryBinding(ctx, expected.ImageRepository)
	if err != nil || got != expected {
		t.Fatalf("forward read: %+v %v", got, err)
	}
	if err := c.ReconcilePublicationRepository(ctx, expected, 42, 7, "acme/app"); err != nil {
		t.Fatal(err)
	}
	want, _ := s.PublicationRepositoryBinding(ctx, expected.ImageRepository)
	got, err = c.PublicationRepositoryBinding(ctx, expected.ImageRepository)
	if err != nil || got != want || got.RepositoryID != 42 {
		t.Fatalf("forward write: %+v %v", got, err)
	}
}

func TestPublicationBindingReserveDurable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "publications.db")
	s, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	image := publicationFixture().Repository
	before, err := s.PublicationRepositoryBinding(ctx, image)
	if err != nil || before != (PublicationBinding{ImageRepository: image}) {
		t.Fatalf("absent: %+v %v", before, err)
	}
	if err := s.ReconcilePublicationRepository(ctx, before, 42, 7, "acme/app"); err != nil {
		t.Fatal(err)
	}
	want := PublicationBinding{ImageRepository: image, SourceRepository: "acme/app", RepositoryID: 42, InstallationID: 7, Revision: 1}
	got, err := s.PublicationRepositoryBinding(ctx, image)
	if err != nil || got != want {
		t.Fatalf("reserved: %+v %v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err = s.PublicationRepositoryBinding(ctx, image)
	if err != nil || got != want {
		t.Fatalf("reopened: %+v %v", got, err)
	}
	if err := s.ReconcilePublicationRepository(ctx, before, 42, 7, "acme/app"); err != nil {
		t.Fatalf("idempotent: %v", err)
	}
}
