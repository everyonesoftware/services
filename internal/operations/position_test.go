package operations

import (
	"path/filepath"
	"testing"
)

func TestQueuePositionsAgreeAcrossSubmissionDetailAndList(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, _ := s.Submit("alpha", "update project", "", "")
	second, _ := s.Submit("beta", "update project", "", "")
	if first.QueuePosition != 1 || second.QueuePosition != 2 {
		t.Fatalf("accepted positions: %d, %d", first.QueuePosition, second.QueuePosition)
	}
	detail, _ := s.Get(second.ID)
	if detail.QueuePosition != 2 {
		t.Fatalf("detail position %d", detail.QueuePosition)
	}
	s.Claim()
	detail, _ = s.Get(second.ID)
	if detail.QueuePosition != 1 {
		t.Fatalf("detail position after claim %d", detail.QueuePosition)
	}
	duplicate, _ := s.Submit("beta", "update project", "", "")
	if duplicate.ID != second.ID || duplicate.QueuePosition != 1 {
		t.Fatalf("duplicate position %+v", duplicate)
	}
	list, _ := s.List()
	if list[0].QueuePosition != detail.QueuePosition {
		t.Fatal("list/detail positions disagree")
	}
}
