package operations

import (
	"path/filepath"
	"testing"
)

func TestDurableFIFOAndIdempotency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operations.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Submit("alpha", "update project", "", "key-1")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.Submit("alpha", "update project", "", "key-1")
	if err != nil || duplicate.ID != first.ID {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	second, err := s.Submit("beta", "update project", "", "key-2")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	claimed, err := s.Claim()
	if err != nil || claimed.ID != first.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	blocked, err := s.Claim()
	if err != nil || blocked != nil {
		t.Fatalf("overlap: %+v %v", blocked, err)
	}
	if err := s.Finish(first.ID, nil, false); err != nil {
		t.Fatal(err)
	}
	claimed, err = s.Claim()
	if err != nil || claimed.ID != second.ID {
		t.Fatalf("FIFO: %+v %v", claimed, err)
	}
}
