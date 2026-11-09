package updates

import (
	"context"
	"strings"
	"testing"
)

func TestCheckerEvictedPublicationFallsBackToRegistry(t *testing.T) {
	store := openPublicationTestStore(t)
	registry := &fakeRegistry{digest: "sha256:" + strings.Repeat("e", 64)}
	checker := NewChecker(registry, 0)
	checker.SetPublicationStore(store)
	publication := publicationFixture()
	if _, err := checker.AcceptPublication(context.Background(), publication); err != nil {
		t.Fatal(err)
	}
	containers := publicationContainers()
	checker.Apply(containers)
	if containers[0].UpdatePublication == nil {
		t.Fatal("fixture publication was not hydrated")
	}
	// Exercise the same tag deletion used by rolling retention, including its FK cascade.
	if _, err := store.db.Exec("DELETE FROM publication_tags WHERE repository=? AND tag=?", publication.Repository, "stable"); err != nil {
		t.Fatal(err)
	}
	if err := checker.CheckNow(context.Background(), containers); err != nil {
		t.Fatal(err)
	}
	checker.Apply(containers)
	if containers[0].ProposedDigest != registry.digest || containers[0].UpdatePublication != nil || containers[0].UpdateCheckedAt == "" {
		t.Fatalf("evicted publication prevented registry fallback: %+v", containers[0])
	}
	if registry.calls != 1 {
		t.Fatalf("registry calls=%d want1", registry.calls)
	}
}
