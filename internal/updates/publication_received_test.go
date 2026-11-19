package updates

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPublicationReceivedAtPreservesOriginalAcceptanceAcrossDuplicateAndRename(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	// A future publishedAt is permitted metadata, never authoritative acceptance time.
	p.PublishedAt = time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	before := time.Now().UTC()
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()
	received, err := s.PublicationEventReceivedAt(ctx, p)
	if err != nil || received.Before(before) || received.After(after) {
		t.Fatalf("invalid first acceptance time %v %v", received, err)
	}
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.PublicationEventReceivedAt(ctx, p)
	if err != nil || !duplicate.Equal(received) {
		t.Fatal("duplicate changed original acceptance time", err)
	}
	binding, err := s.PublicationRepositoryBinding(ctx, p.Repository)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcilePublicationRepository(ctx, binding, 12, 7, "acme/renamed"); err != nil {
		t.Fatal(err)
	}
	historical, err := s.PublicationEventReceivedAt(ctx, p)
	if err != nil || !historical.Equal(received) {
		t.Fatal("reconciliation changed receipt evidence", err)
	}
	checker := NewChecker(nil, 0)
	checker.SetPublicationStore(s)
	forwarded, err := checker.PublicationEventReceivedAt(ctx, p)
	if err != nil || !forwarded.Equal(received) {
		t.Fatal("checker lost receipt evidence", err)
	}
}

func TestPublicationReceivedAtFailsClosedForChangedOrMissingEvidence(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	changed := p
	changed.Version = "different"
	if _, err := s.PublicationEventReceivedAt(ctx, changed); !errors.Is(err, ErrPublicationConflict) {
		t.Fatal("changed payload certified", err)
	}
	reversed := p
	reversed.Tags = []string{p.Tags[1], p.Tags[0]}
	if received, err := s.PublicationEventReceivedAt(ctx, reversed); err != nil || received.IsZero() {
		t.Fatal("tag-order normalization failed", err)
	}
	missing := p
	missing.RunID = "9999"
	missing.RunURL = "https://github.com/acme/app/actions/runs/9999"
	if received, err := s.PublicationEventReceivedAt(ctx, missing); err != nil || !received.IsZero() {
		t.Fatal("missing evidence fabricated", err)
	}
	if _, err := NewChecker(nil, 0).PublicationEventReceivedAt(ctx, p); err == nil {
		t.Fatal("missing store certified")
	}
}
