package auth

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordMinimumCountsCharactersNotUTF8Bytes(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Bootstrap("admin", strings.Repeat("界", 5)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("five characters must not satisfy a fifteen-character minimum: %v", err)
	}
	if _, err := s.Bootstrap("admin", strings.Repeat("界", 15)); err != nil {
		t.Fatal(err)
	}
}
