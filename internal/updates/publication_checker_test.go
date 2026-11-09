package updates

import (
	"context"
	"path/filepath"
	"services/internal/groups"
	"strings"
	"testing"
	"time"
)

func publicationContainers() []groups.Container {
	return []groups.Container{{Project: "media", Image: "ghcr.io/acme/app:stable", State: "running", RepoDigests: []string{"ghcr.io/acme/app@sha256:" + strings.Repeat("0", 64)}}}
}
func TestCheckerPublicationPersistsHydratesAndCopies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publications.db")
	s, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	registry := &signalingRegistry{calls: make(chan string, 10)}
	c := NewChecker(registry, 0)
	c.SetPublicationStore(s)
	p := publicationFixture()
	if got, err := c.AcceptPublication(context.Background(), p); err != nil || got != "accepted" {
		t.Fatalf("accept=%s %v", got, err)
	}
	p.Tags[0] = "mutated"
	items := publicationContainers()
	c.Apply(items)
	if !items[0].UpdateAvailable || items[0].UpdatePublication == nil || items[0].ProposedDigest != p.Digest || items[0].UpdateCheckedAt != "" {
		t.Fatalf("notification not applied: %+v", items[0])
	}
	items[0].UpdatePublication.Tags[0] = "output-mutated"
	items[0].UpdatePublication.Digest = "mutated"
	c.Apply(items)
	if items[0].UpdatePublication.Digest != p.Digest || items[0].UpdatePublication.Tags[0] == "output-mutated" {
		t.Fatal("publication aliases caller memory")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c = NewChecker(registry, 0)
	c.SetPublicationStore(s)
	items = publicationContainers()
	c.Apply(items)
	if !items[0].UpdateAvailable || items[0].UpdatePublication == nil {
		t.Fatalf("restart hydration failed: %+v", items[0])
	}
	select {
	case image := <-registry.calls:
		t.Fatalf("fresh publication caused registry call: %s", image)
	case <-time.After(20 * time.Millisecond):
	}
}
func TestCheckerPublicationEligibilityAndTagMatching(t *testing.T) {
	s := openPublicationTestStore(t)
	c := NewChecker(nil, 0)
	c.SetPublicationStore(s)
	p := publicationFixture()
	if _, err := c.AcceptPublication(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	eligible := publicationContainers()[0]
	items := []groups.Container{eligible, eligible, eligible, eligible, eligible, eligible, eligible}
	items[1].Image = "ghcr.io/acme/app:other"
	items[2].State = "exited"
	items[3].Image = "ghcr.io/acme/other:stable"
	items[4].Image = p.Repository + "@" + p.Digest
	items[5].RepoDigests = nil
	items[6].Project = ""
	// Ensure stale caller-provided metadata is cleared on every path.
	for i := range items {
		v := p
		items[i].UpdatePublication = &v
	}
	c.Apply(items)
	if !items[0].UpdateAvailable || items[0].UpdatePublication == nil {
		t.Fatal("eligible tag missing")
	}
	for i := 1; i < len(items); i++ {
		if items[i].UpdateAvailable || items[i].UpdatePublication != nil {
			t.Fatalf("ineligible %d: %+v", i, items[i])
		}
	}
	items = publicationContainers()
	items[0].RepoDigests = []string{p.Repository + "@" + p.Digest}
	c.Apply(items)
	if items[0].UpdateAvailable || items[0].UpdateStatus != "current" {
		t.Fatalf("equal digest not current: %+v", items[0])
	}
}
func TestCheckerPublicationSharedStoreReadAndRegistryReconciliation(t *testing.T) {
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
	producer := NewChecker(nil, 0)
	producer.SetPublicationStore(a)
	registry := &fakeRegistry{digest: "sha256:" + strings.Repeat("c", 64)}
	reader := NewChecker(registry, 0)
	reader.SetPublicationStore(b)
	p := publicationFixture()
	if _, err := producer.AcceptPublication(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	items := publicationContainers()
	reader.Apply(items)
	if !items[0].UpdateAvailable || items[0].UpdatePublication == nil {
		t.Fatal("shared store notification invisible")
	}
	if err := reader.CheckNow(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	reader.Apply(items)
	if items[0].ProposedDigest != registry.digest || items[0].UpdatePublication != nil {
		t.Fatalf("obsolete metadata after reconciliation: %+v", items[0])
	}
	// Do not rehydrate the same event and undo explicit reconciliation.
	reader.Apply(items)
	if items[0].UpdatePublication != nil || items[0].ProposedDigest != registry.digest {
		t.Fatal("reconciliation undone")
	}
	p.RunID = "124"
	p.RunURL = "https://github.com/acme/app/actions/runs/124"
	p.PublishedAt = "2026-11-09T13:00:00Z"
	if _, err := producer.AcceptPublication(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	reader.Apply(items)
	registry.digest = p.Digest
	if err := reader.CheckNow(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	reader.Apply(items)
	if items[0].UpdatePublication == nil {
		t.Fatal("matching registry result lost publication provenance")
	}
}

type blockedPublicationRegistry struct {
	started chan struct{}
	release chan struct{}
	digest  string
}

func (r *blockedPublicationRegistry) ManifestDigest(context.Context, string) (string, error) {
	close(r.started)
	<-r.release
	return r.digest, nil
}
func TestCheckerInFlightRegistryCannotOverwritePublication(t *testing.T) {
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
	registry := &blockedPublicationRegistry{started: make(chan struct{}), release: make(chan struct{}), digest: "sha256:obsolete"}
	c := NewChecker(registry, 0)
	c.SetPublicationStore(a)
	items := publicationContainers()
	done := make(chan error, 1)
	go func() { done <- c.CheckNow(context.Background(), items) }()
	<-registry.started
	producer := NewChecker(nil, 0)
	producer.SetPublicationStore(b)
	p := publicationFixture()
	if _, err := producer.AcceptPublication(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	close(registry.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	c.Apply(items)
	if items[0].ProposedDigest != p.Digest || items[0].UpdatePublication == nil {
		t.Fatalf("old in-flight result overwrote notification: %+v", items[0])
	}
}
func TestCheckerAcceptRequiresDurableStore(t *testing.T) {
	c := NewChecker(nil, 0)
	if _, err := c.AcceptPublication(context.Background(), publicationFixture()); err == nil {
		t.Fatal("accepted notification without durability")
	}
}
