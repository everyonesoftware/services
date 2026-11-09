package updates

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicationObservationRejectsFutureSchemaOnOpenStore(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	record, err := s.current(ctx, p.Repository, "stable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.observe(ctx, p.Repository, "stable", record.ID, "sha256:corrected", time.Now()); err == nil || ok {
		t.Fatalf("future schema observation accepted: %v %v", ok, err)
	}
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM publication_observations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("future schema write left %d observations", n)
	}
}

func TestPublicationObservationFencesEventsAndCascadesOnTagDeletion(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	old, err := s.current(ctx, p.Repository, "stable")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if ok, err := s.observe(ctx, p.Repository, "stable", old.ID, "sha256:corrected", at); err != nil || !ok {
		t.Fatalf("observe: %v %v", ok, err)
	}
	if digest, got, err := s.loadObservation(ctx, p.Repository, "stable", old.ID); err != nil || digest != "sha256:corrected" || !got.Equal(at) {
		t.Fatalf("load: %s %s %v", digest, got, err)
	}
	if digest, _, err := s.loadObservation(ctx, p.Repository, "latest", old.ID); err != nil || digest != "" {
		t.Fatalf("observation leaked across tags: %s %v", digest, err)
	}
	p.RunID = "124"
	p.RunURL = "https://github.com/acme/app/actions/runs/124"
	p.PublishedAt = "2026-11-09T13:00:00Z"
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	current, err := s.current(ctx, p.Repository, "stable")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.observe(ctx, p.Repository, "stable", old.ID, "sha256:stale", at.Add(time.Second)); err != nil || ok {
		t.Fatalf("old event write not fenced: %v %v", ok, err)
	}
	if digest, _, err := s.loadObservation(ctx, p.Repository, "stable", current.ID); err != nil || digest != "" {
		t.Fatalf("old observation attached to new event: %s %v", digest, err)
	}
	if _, err := s.db.Exec("DELETE FROM publication_tags WHERE repository=? AND tag=?", p.Repository, "stable"); err != nil {
		t.Fatal(err)
	}
	if digest, _, err := s.loadObservation(ctx, p.Repository, "stable", old.ID); err != nil || digest != "" {
		t.Fatalf("deleted tag retained observation: %s %v", digest, err)
	}
}

func TestPublicationMatchingObservationRetainsProvenanceAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publications.db")
	s, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	p := publicationFixture()
	c := NewChecker(&fakeRegistry{digest: p.Digest}, 0)
	c.SetPublicationStore(s)
	if _, err := c.AcceptPublication(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckNow(context.Background(), publicationContainers()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c = NewChecker(nil, 0)
	c.SetPublicationStore(s)
	items := publicationContainers()
	c.Apply(items)
	if items[0].UpdatePublication == nil || items[0].UpdateCheckedAt == "" || items[0].ProposedDigest != p.Digest {
		t.Fatalf("matching observation lost metadata: %+v", items[0])
	}
}

func TestPublicationObservationWriteFailureDoesNotReportCheckSuccess(t *testing.T) {
	s := openPublicationTestStore(t)
	c := NewChecker(&fakeRegistry{digest: "sha256:corrected"}, 0)
	c.SetPublicationStore(s)
	if _, err := c.AcceptPublication(context.Background(), publicationFixture()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_observation BEFORE INSERT ON publication_observations BEGIN SELECT RAISE(ABORT,'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckNow(context.Background(), publicationContainers()); err == nil {
		t.Fatal("check reported success without durable observation")
	}
	items := publicationContainers()
	c.Apply(items)
	if items[0].UpdatePublication == nil || items[0].UpdateCheckedAt != "" {
		t.Fatalf("failed durable observation applied in memory: %+v", items[0])
	}
}

func TestPublicationReceiptIsNotRegistryCheckTimestamp(t *testing.T) {
	s := openPublicationTestStore(t)
	c := NewChecker(nil, 0)
	c.SetPublicationStore(s)
	if _, err := c.AcceptPublication(context.Background(), publicationFixture()); err != nil {
		t.Fatal(err)
	}
	items := publicationContainers()
	c.Apply(items)
	if items[0].UpdateCheckedAt != "" {
		t.Fatalf("notification receipt masquerades as registry check: %q", items[0].UpdateCheckedAt)
	}
}

func TestPublicationReconciliationSurvivesRestartAndSharedReaders(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "publications.db")
	s, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := &signalingRegistry{digest: "sha256:" + strings.Repeat("c", 64), calls: make(chan string, 10)}
	writer := NewChecker(registry, 0)
	writer.SetPublicationStore(s)
	p := publicationFixture()
	if _, err := writer.AcceptPublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	sharedStore, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sharedStore.Close()
	shared := NewChecker(registry, 0)
	shared.SetPublicationStore(sharedStore)
	items := publicationContainers()
	shared.Apply(items) // Cache the same event before reconciliation.
	if err := writer.CheckNow(ctx, items); err != nil {
		t.Fatal(err)
	}
	<-registry.calls
	shared.Apply(items)
	if items[0].ProposedDigest != registry.digest || items[0].UpdatePublication != nil {
		t.Errorf("shared reader reverted registry correction: %+v", items[0])
	}
	if err := sharedStore.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reader := NewChecker(registry, 0)
	reader.SetPublicationStore(s)
	items = publicationContainers()
	reader.Apply(items)
	if items[0].ProposedDigest != registry.digest || items[0].UpdatePublication != nil {
		t.Fatalf("restart reverted registry correction: %+v", items[0])
	}
	if items[0].UpdateCheckedAt == "" {
		t.Fatal("persisted registry check timestamp missing")
	}
	p.RunID = "124"
	p.RunURL = "https://github.com/acme/app/actions/runs/124"
	p.PublishedAt = "2026-11-09T13:00:00Z"
	if _, err := reader.AcceptPublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	reader.Apply(items)
	if items[0].ProposedDigest != p.Digest || items[0].UpdatePublication == nil || items[0].UpdateCheckedAt != "" {
		t.Fatalf("new notification did not supersede observation: %+v", items[0])
	}
	select {
	case image := <-registry.calls:
		t.Fatalf("hydration unexpectedly queried registry: %s", image)
	case <-time.After(20 * time.Millisecond):
	}
}
