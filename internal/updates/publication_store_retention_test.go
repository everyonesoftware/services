package updates

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPublicationStoreDelayedEventsApplyOnlyFresherTags(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	newer := publicationFixture()
	newer.Tags = []string{"v1"}
	newer.RunID = "125"
	newer.RunURL = "https://github.com/acme/app/actions/runs/125"
	newer.PublishedAt = "2026-11-09T13:00:00Z"
	if got, err := s.accept(ctx, newer); err != nil || got != "accepted" {
		t.Fatalf("newer version event: %s %v", got, err)
	}
	delayed := publicationFixture()
	delayed.Tags = []string{"latest", "v1", "delayed-new-tag"}
	if got, err := s.accept(ctx, delayed); err != nil || got != "accepted" {
		t.Fatalf("delayed mixed event: %s %v", got, err)
	}
	for tag, run := range map[string]string{"latest": delayed.RunID, "delayed-new-tag": delayed.RunID, "v1": newer.RunID} {
		r, err := s.current(ctx, delayed.Repository, tag)
		if err != nil || r == nil || r.Publication.RunID != run {
			t.Fatalf("tag %q: %+v %v; want run %s", tag, r, err, run)
		}
	}
	if got, err := s.accept(ctx, delayed); err != nil || got != "duplicate" {
		t.Fatalf("delayed identity not persisted: %s %v", got, err)
	}
	stale := delayed
	stale.RunID = "124"
	stale.RunURL = "https://github.com/acme/app/actions/runs/124"
	stale.Tags = []string{"v1"}
	if got, err := s.accept(ctx, stale); err != nil || got != "stale" {
		t.Fatalf("all overlapping tags stale: %s %v", got, err)
	}
}

func TestPublicationStoreRollingTagRetention(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	first := publicationFixture()
	base, _ := time.Parse(time.RFC3339Nano, first.PublishedAt)
	p := first
	for i := 0; i < maxPublicationEvents+8; i++ {
		p = publicationFixture()
		p.RunID = fmt.Sprint(123 + i)
		p.RunURL = "https://github.com/acme/app/actions/runs/" + p.RunID
		p.PublishedAt = base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		p.Tags = []string{"latest", "main", fmt.Sprintf("v%d", i), fmt.Sprintf("sha-%040x", i)}
		if i == 0 {
			first = p
		}
		if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
			t.Fatalf("rolling publication %d: %s %v", i, got, err)
		}
		for _, tag := range p.Tags {
			r, err := s.current(ctx, p.Repository, tag)
			if err != nil || r == nil || r.Publication.RunID != p.RunID {
				t.Fatalf("incoming tag %q not retained at publication %d: %+v %v", tag, i, r, err)
			}
		}
		var tags int
		if err := s.db.QueryRow("SELECT count(*) FROM publication_tags WHERE repository=?", p.Repository).Scan(&tags); err != nil {
			t.Fatal(err)
		}
		want := min(2+2*(i+1), maxPublicationTags)
		if tags != want {
			t.Fatalf("publication %d retained %d tags, want %d", i, tags, want)
		}
	}
	for _, tag := range []string{"v0", fmt.Sprintf("sha-%040x", 0)} {
		if r, err := s.current(ctx, p.Repository, tag); err != nil || r != nil {
			t.Fatalf("oldest tag %q not evicted: %+v %v", tag, r, err)
		}
	}
	var events int
	if err := s.db.QueryRow("SELECT count(*) FROM publication_events WHERE repository=?", p.Repository).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != maxPublicationEvents {
		t.Fatalf("retained %d events, want %d", events, maxPublicationEvents)
	}
	// Even changed timestamps/payloads cannot resurrect identities whose tags
	// were evicted and whose events were subsequently pruned.
	first.PublishedAt = base.Add(time.Duration(maxPublicationEvents+20) * time.Second).Format(time.RFC3339Nano)
	first.Digest = "sha256:" + strings.Repeat("d", 64)
	first.Tags = []string{"resurrected"}
	if got, err := s.accept(ctx, first); err != nil || got != "stale" {
		t.Fatalf("pruned rolling identity: %s %v", got, err)
	}
	if r, err := s.current(ctx, first.Repository, "resurrected"); err != nil || r != nil {
		t.Fatalf("pruned replay restored tag: %+v %v", r, err)
	}
}

func TestPublicationStoreTagRetentionTieBreakAndIncomingBatch(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	for batch := 0; batch < 4; batch++ {
		p.RunID = fmt.Sprint(123 + batch)
		p.RunURL = "https://github.com/acme/app/actions/runs/" + p.RunID
		p.Tags = nil
		for i := 0; i < 64; i++ {
			p.Tags = append(p.Tags, fmt.Sprintf("tag-%03d", batch*64+i))
		}
		if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
			t.Fatalf("seed batch %d: %s %v", batch, got, err)
		}
	}
	// Refresh the lexically first old tag; evict its next sibling instead.
	p.RunID = "127"
	p.RunURL = "https://github.com/acme/app/actions/runs/127"
	p.Tags = []string{"tag-000", "new"}
	if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
		t.Fatalf("refresh and add: %s %v", got, err)
	}
	for _, tag := range []string{"tag-000", "tag-002", "new"} {
		if r, err := s.current(ctx, p.Repository, tag); err != nil || r == nil {
			t.Fatalf("retained tag %q missing: %+v %v", tag, r, err)
		}
	}
	if r, err := s.current(ctx, p.Repository, "tag-001"); err != nil || r != nil {
		t.Fatalf("tie-break did not evict least tag: %+v %v", r, err)
	}
	p.RunID = "128"
	p.RunURL = "https://github.com/acme/app/actions/runs/128"
	p.Tags = nil
	for i := 0; i < 64; i++ {
		p.Tags = append(p.Tags, fmt.Sprintf("incoming-%03d", i))
	}
	if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
		t.Fatalf("full incoming batch: %s %v", got, err)
	}
	for _, tag := range p.Tags {
		if r, err := s.current(ctx, p.Repository, tag); err != nil || r == nil || r.Publication.RunID != p.RunID {
			t.Fatalf("incoming tag %q missing: %+v %v", tag, r, err)
		}
	}
	var tags int
	if err := s.db.QueryRow("SELECT count(*) FROM publication_tags WHERE repository=?", p.Repository).Scan(&tags); err != nil {
		t.Fatal(err)
	}
	if tags != maxPublicationTags {
		t.Fatalf("retained %d tags, want %d", tags, maxPublicationTags)
	}
}

func TestPublicationStoreTagEvictionFailureRollsBackWholeEvent(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	for batch := 0; batch < 4; batch++ {
		p.RunID = fmt.Sprint(123 + batch)
		p.RunURL = "https://github.com/acme/app/actions/runs/" + p.RunID
		p.Tags = nil
		for i := 0; i < 64; i++ {
			p.Tags = append(p.Tags, fmt.Sprintf("tag-%03d", batch*64+i))
		}
		if _, err := s.accept(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.current(ctx, p.Repository, "tag-000")
	if err != nil || before == nil {
		t.Fatalf("seed tag missing: %+v %v", before, err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_eviction BEFORE DELETE ON publication_tags BEGIN SELECT RAISE(ABORT,'blocked eviction'); END`); err != nil {
		t.Fatal(err)
	}
	p.RunID = "127"
	p.RunURL = "https://github.com/acme/app/actions/runs/127"
	p.Tags = []string{"tag-000", "new"}
	if _, err := s.accept(ctx, p); err == nil || !strings.Contains(err.Error(), "blocked eviction") {
		t.Fatalf("expected forced eviction failure, got %v", err)
	}
	after, err := s.current(ctx, p.Repository, "tag-000")
	if err != nil || after == nil || after.ID != before.ID {
		t.Fatalf("failed eviction updated current tag: %+v %v", after, err)
	}
	if r, err := s.current(ctx, p.Repository, "new"); err != nil || r != nil {
		t.Fatalf("failed eviction retained incoming tag: %+v %v", r, err)
	}
	for table, want := range map[string]int{"publication_events": 4, "publication_tags": maxPublicationTags, "publication_repositories": 1, "publication_replay_floors": 0} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != want {
			t.Fatalf("rollback %s: count=%d want=%d err=%v", table, count, want, err)
		}
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_eviction"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
		t.Fatalf("retry after eviction rollback: %s %v", got, err)
	}
}

func TestPublicationStoreRepositoryCapacityStillRejectsAtomically(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	for i := 0; i < maxPublicationRepositories; i++ {
		p.Repository = fmt.Sprintf("ghcr.io/acme/app-%d", i)
		if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
			t.Fatalf("seed repository %d: %s %v", i, got, err)
		}
	}
	p.Repository = "ghcr.io/acme/overflow"
	if _, err := s.accept(ctx, p); err == nil || !strings.Contains(err.Error(), "repository capacity") {
		t.Fatalf("expected repository capacity failure, got %v", err)
	}
	for _, table := range []string{"publication_events", "publication_tags", "publication_repositories"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM "+table+" WHERE repository=?", p.Repository).Scan(&count); err != nil || count != 0 {
			t.Fatalf("capacity failure partially wrote %s: %d %v", table, count, err)
		}
	}
}
