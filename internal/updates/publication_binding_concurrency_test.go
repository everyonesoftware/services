package updates

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestPublicationBindingConcurrentCASAcrossStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "publications.db")
	a, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	expected := PublicationBinding{ImageRepository: publicationFixture().Repository}
	if err := a.ReconcilePublicationRepository(ctx, expected, 42, 7, "acme/original"); err != nil {
		t.Fatal(err)
	}
	expected, err = a.PublicationRepositoryBinding(ctx, expected.ImageRepository)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for i, s := range []*PublicationStore{a, b} {
		source := []string{"acme/first", "acme/second"}[i]
		go func() { <-start; results <- s.ReconcilePublicationRepository(ctx, expected, 42, 7, source) }()
	}
	close(start)
	succeeded, conflicted := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrPublicationConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("CAS: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	winner, err := a.PublicationRepositoryBinding(ctx, expected.ImageRepository)
	if err != nil || winner.Revision != 2 {
		t.Fatalf("winner: %+v %v", winner, err)
	}
	// Returning to the original source is still a new revision, not an ABA bypass.
	if err := b.ReconcilePublicationRepository(ctx, winner, 42, 7, "acme/original"); err != nil {
		t.Fatal(err)
	}
	if err := a.ReconcilePublicationRepository(ctx, expected, 42, 7, "acme/stale"); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("ABA stale CAS: %v", err)
	}
	reopened, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.PublicationRepositoryBinding(ctx, expected.ImageRepository)
	if err != nil || got.Revision != 3 || got.SourceRepository != "acme/original" || got.RepositoryID != 42 || got.InstallationID != 7 {
		t.Fatalf("durable renames: %+v %v", got, err)
	}
}

func TestPublicationBindingReconciliationFailureRollsBackSource(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	expected, err := s.PublicationRepositoryBinding(ctx, p.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_binding BEFORE INSERT ON publication_repository_identities BEGIN SELECT RAISE(ABORT,'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePublicationRepository(ctx, expected, 42, 7, "acme/renamed"); err == nil {
		t.Fatal("forced failure missing")
	}
	if got, err := s.PublicationRepositoryBinding(ctx, p.Repository); err != nil || got != expected {
		t.Fatalf("partial migration: %+v %v", got, err)
	}
}

func TestPublicationBindingRenamedFloorRemainsStale(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("INSERT INTO publication_replay_floors(repository,run_id,attempt) VALUES(?,?,?)", p.Repository, "122", 2); err != nil {
		t.Fatal(err)
	}
	before, err := s.current(ctx, p.Repository, "latest")
	if err != nil {
		t.Fatal(err)
	}
	expected, _ := s.PublicationRepositoryBinding(ctx, p.Repository)
	if err := s.ReconcilePublicationRepository(ctx, expected, 42, 7, "acme/renamed"); err != nil {
		t.Fatal(err)
	}
	p.SourceRepository = "acme/renamed"
	p.RunID = "122"
	p.RunAttempt = 2
	p.RunURL = "https://github.com/acme/renamed/actions/runs/122"
	p.PublishedAt = "2026-11-09T13:00:00Z"
	p.Tags = []string{"new"}
	if got, err := s.accept(ctx, p); err != nil || got != "stale" {
		t.Fatalf("renamed floor bypass: %s %v", got, err)
	}
	after, err := s.current(ctx, p.Repository, "latest")
	if err != nil || after.ID != before.ID || after.ReceivedAt != before.ReceivedAt {
		t.Fatalf("floor changed existing tag: %+v %v", after, err)
	}
	if got, err := s.current(ctx, p.Repository, "new"); err != nil || got != nil {
		t.Fatalf("floor created tag: %+v %v", got, err)
	}
}
