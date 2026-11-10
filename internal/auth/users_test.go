package auth

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestUsersLifecycleAndPasswordSessionRevocation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.Bootstrap("admin", password)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteUser(first.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("last admin deletion=%v", err)
	}
	second, err := s.CreateUser("other", password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateUser(" OTHER ", password); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate=%v", err)
	}
	users, err := s.ListUsers()
	if err != nil || len(users) != 2 {
		t.Fatalf("users=%v %v", users, err)
	}
	_, session, err := s.Login("other", password)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ChangePassword(second.ID, "a different long password"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Session(session); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("reset session=%v", err)
	}
	if _, _, err = s.Login("other", password); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old password=%v", err)
	}
	_, session, err = s.Login("other", "a different long password")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteUser(second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Session(session); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted session=%v", err)
	}
	if err = s.DeleteUser("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user=%v", err)
	}
	if err = s.ChangePassword(first.ID, "short"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("weak password=%v", err)
	}
}

func TestConcurrentAdminDeletionAlwaysKeepsOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	first, err := a.Bootstrap("one", password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.CreateUser("two", password)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(s *Store, id string) { defer wg.Done(); results <- s.DeleteUser(id) }([]*Store{a, b}[i], id)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("deletions=%d conflicts=%d", success, conflict)
	}
}
