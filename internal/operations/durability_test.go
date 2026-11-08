package operations

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestCoalescedIdempotencyKeyRemainsBoundAfterCompletion(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "ops.db"))
	defer s.Close()
	first, _ := s.Submit("a", "update project", "", "first")
	alias, err := s.Submit("a", "update project", "", "alias")
	if err != nil || alias.ID != first.ID {
		t.Fatal(alias, err)
	}
	s.Claim()
	s.Finish(first.ID, nil, false)
	again, err := s.Submit("a", "update project", "", "alias")
	if err != nil || again.ID != first.ID {
		t.Fatalf("coalesced key replayed: %s instead of %s (%v)", again.ID, first.ID, err)
	}
}
func TestCrossConnectionClaimSerializesAndBoundsPersistedLogs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ops.db")
	a, _ := Open(path)
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	o, _ := a.Submit("a", "update project", "", "")
	a.Submit("b", "update project", "", "")
	var wg sync.WaitGroup
	claimed := make(chan string, 2)
	for _, s := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			o, err := s.Claim()
			if err != nil {
				t.Error(err)
			}
			if o != nil {
				claimed <- o.ID
			}
		}(s)
	}
	wg.Wait()
	close(claimed)
	if len(claimed) != 1 {
		t.Fatalf("global overlap: %d", len(claimed))
	}
	for i := 0; i < MaxMessages+5; i++ {
		if err := a.Message(o.ID, fmt.Sprint(i)+strings.Repeat("界", 2000)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := b.Get(o.ID)
	if err != nil || len(got.Messages) != MaxMessages {
		t.Fatal(len(got.Messages), err)
	}
	for _, line := range got.Messages {
		if len(line) > MaxMessageBytes || !utf8.ValidString(line) {
			t.Fatal("invalid bounded log")
		}
	}
}
func TestUnknownSchemaRefusesOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ops.db")
	s, _ := Open(path)
	s.db.Exec(`PRAGMA user_version=99`)
	s.Close()
	if store, err := Open(path); err == nil {
		store.Close()
		t.Fatal("unknown schema accepted")
	}
}
