package updates

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicationStoreTagWriteFailureRollsBackWholeEvent(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	if _, err := s.db.Exec(`CREATE TRIGGER reject_tag BEFORE INSERT ON publication_tags WHEN NEW.tag='stable' BEGIN SELECT RAISE(ABORT,'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.accept(ctx, p); err == nil {
		t.Fatal("expected forced tag failure")
	}
	for _, table := range []string{"publication_events", "publication_tags", "publication_repositories"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("partial write in %s", table)
		}
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_tag"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
		t.Fatalf("retry after rollback: %s %v", got, err)
	}
}

func TestPublicationStoreConcurrentReplayIsAcceptedOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publications.db")
	stores := make([]*PublicationStore, 8)
	for i := range stores {
		s, err := OpenPublicationStore(path)
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = s
		defer s.Close()
	}
	type outcome struct {
		result string
		err    error
	}
	done := make(chan outcome, len(stores))
	start := make(chan struct{})
	for _, s := range stores {
		go func(s *PublicationStore) {
			<-start
			r, e := s.accept(context.Background(), publicationFixture())
			done <- outcome{r, e}
		}(s)
	}
	close(start)
	accepted := 0
	for range stores {
		out := <-done
		if out.err != nil {
			t.Fatal(out.err)
		}
		switch out.result {
		case "accepted":
			accepted++
		case "duplicate":
		default:
			t.Fatalf("unexpected result %q", out.result)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d times", accepted)
	}
}
func TestPublicationStoreReplayDoesNotFreshenReceipt(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	before, err := s.current(ctx, p.Repository, "stable")
	if err != nil {
		t.Fatal(err)
	}
	p.Tags = []string{"latest", "stable"}
	if got, err := s.accept(ctx, p); err != nil || got != "duplicate" {
		t.Fatalf("reordered tags replay: %s %v", got, err)
	}
	after, err := s.current(ctx, p.Repository, "stable")
	if err != nil {
		t.Fatal(err)
	}
	if before.ID != after.ID || !before.ReceivedAt.Equal(after.ReceivedAt) {
		t.Fatal("replay freshened receipt")
	}
}
func TestPublicationStoreSchemaAndPathFailClosed(t *testing.T) {
	if s, err := OpenPublicationStore(filepath.Join(t.TempDir(), "operations.db")); err == nil {
		s.Close()
		t.Fatal("operations DB accepted")
	}
	path := filepath.Join(t.TempDir(), "publications.db")
	s, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.accept(context.Background(), publicationFixture()); err == nil {
		t.Fatal("write used future schema")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := OpenPublicationStore(path); err == nil {
		s.Close()
		t.Fatal("opened future schema")
	}
}

func TestPublicationStorePublishedAtWinsOverRunCreationOrder(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.RunID = "122"
	p.RunAttempt = 2
	p.RunURL = "https://github.com/acme/app/actions/runs/122"
	p.PublishedAt = "2026-11-09T13:00:00Z"
	if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
		t.Fatalf("later republish of earlier-created run: %s %v", got, err)
	}
}
func TestPublicationStoreRejectsFutureClockPoisoning(t *testing.T) {
	s := openPublicationTestStore(t)
	p := publicationFixture()
	p.PublishedAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := s.accept(context.Background(), p); err == nil {
		t.Fatal("future publication poisoned ordering")
	}
	p.PublishedAt = time.Now().Add(4 * time.Minute).UTC().Format(time.RFC3339Nano)
	if got, err := s.accept(context.Background(), p); err != nil || got != "accepted" {
		t.Fatalf("modest clock skew: %s %v", got, err)
	}
}

func TestPublicationStoreRollingRetentionAndDurableHighWater(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publications.db")
	s, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	ctx := context.Background()
	first := publicationFixture()
	first.Tags = []string{"historical"}
	if _, err := s.accept(ctx, first); err != nil {
		t.Fatal(err)
	}
	base, _ := time.Parse(time.RFC3339Nano, first.PublishedAt)
	var evicted = publicationFixture()
	for i := 0; i < maxPublicationEvents+8; i++ {
		p := publicationFixture()
		p.RunID = fmt.Sprint(124 + i)
		p.RunURL = "https://github.com/acme/app/actions/runs/" + p.RunID
		p.PublishedAt = base.Add(time.Duration(i+1) * time.Second).Format(time.RFC3339Nano)
		if i == 0 {
			evicted = p
		}
		if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
			t.Fatalf("publication %d: %s %v", i, got, err)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM publication_events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count > maxPublicationEvents+1 {
		t.Fatalf("unbounded replay retention: %d", count)
	}
	if r, err := s.current(ctx, first.Repository, "historical"); err != nil || r == nil {
		t.Fatalf("referenced old event pruned: %v %v", r, err)
	}
	if got, err := s.accept(ctx, first); err != nil || got != "duplicate" {
		t.Fatalf("retained tombstone: %s %v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	evicted.Tags = []string{"previously-unseen-tag"}
	evicted.PublishedAt = base.Add(time.Duration(maxPublicationEvents+20) * time.Second).Format(time.RFC3339Nano)
	evicted.Digest = "sha256:" + strings.Repeat("d", 64)
	if got, err := s.accept(ctx, evicted); err != nil || got != "stale" {
		t.Fatalf("evicted identity must stay stale: %s %v", got, err)
	}
	if r, err := s.current(ctx, evicted.Repository, "previously-unseen-tag"); err != nil || r != nil {
		t.Fatalf("evicted replay mutated tags: %v %v", r, err)
	}
}
func TestPublicationConflictSentinel(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Digest = "sha256:" + strings.Repeat("c", 64)
	if _, err := s.accept(ctx, p); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("replay conflict: %v", err)
	}
	p = publicationFixture()
	p.SourceRepository = "other/repo"
	p.RunURL = "https://github.com/other/repo/actions/runs/123"
	if _, err := s.accept(ctx, p); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("source conflict: %v", err)
	}
}

func openPublicationTestStore(t *testing.T) *PublicationStore {
	t.Helper()
	s, err := OpenPublicationStore(filepath.Join(t.TempDir(), "publications.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestPublicationStoreDurableReplayAndAtomicTags(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "publications.db")
	s, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	p := publicationFixture()
	if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
		t.Fatalf("accept=%s %v", got, err)
	}
	p.Tags[0] = "mutated"
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	original := publicationFixture()
	for _, tag := range original.Tags {
		r, err := s.current(ctx, original.Repository, tag)
		if err != nil || r == nil || r.Publication.Digest != original.Digest || r.ReceivedAt.IsZero() {
			t.Fatalf("missing durable tag %s: %+v %v", tag, r, err)
		}
	}
	if got, err := s.accept(ctx, original); err != nil || got != "duplicate" {
		t.Fatalf("replay=%s %v", got, err)
	}
	original.Digest = "sha256:" + strings.Repeat("c", 64)
	if _, err := s.accept(ctx, original); err == nil {
		t.Fatal("conflicting replay accepted")
	}
}
func TestPublicationStoreRejectsStaleAndOwnershipChange(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	p.RunAttempt = 2
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(){
		"older timestamp":            func() { p.RunID = "124"; p.PublishedAt = "2026-11-08T12:00:00Z" },
		"older run tied timestamp":   func() { p.RunID = "122"; p.RunAttempt = 5; p.PublishedAt = "2026-11-09T13:00:00Z" },
		"older retry tied timestamp": func() { p.RunAttempt = 1; p.PublishedAt = "2026-11-09T13:00:00Z" },
	}
	// Establish the newer attempt before testing older retries.
	p.RunAttempt = 3
	p.PublishedAt = "2026-11-09T13:00:00Z"
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p = publicationFixture()
			mutate()
			p.RunURL = "https://github.com/" + p.SourceRepository + "/actions/runs/" + p.RunID
			got, err := s.accept(ctx, p)
			if err != nil || got != "stale" {
				t.Fatalf("got %s %v", got, err)
			}
			r, err := s.current(ctx, p.Repository, "latest")
			if err != nil || r == nil || r.Publication.RunID != "123" || r.Publication.RunAttempt != 3 {
				t.Fatalf("stale event changed current tag: %+v %v", r, err)
			}
		})
	}
	p = publicationFixture()
	p.SourceRepository = "other/source"
	p.RunURL = "https://github.com/other/source/actions/runs/123"
	if _, err := s.accept(ctx, p); err == nil {
		t.Fatal("repository owner changed")
	}
}
func TestPublicationStoreSharedConnectionsAndInvalidInput(t *testing.T) {
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
	p := publicationFixture()
	if _, err := a.accept(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	r, err := b.current(context.Background(), p.Repository, "latest")
	if err != nil || r == nil {
		t.Fatalf("shared read: %v %v", r, err)
	}
	p.Tags = nil
	if _, err := a.accept(context.Background(), p); err == nil {
		t.Fatal("invalid input accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.accept(ctx, publicationFixture()); err == nil {
		t.Fatal("cancel ignored")
	}
}
