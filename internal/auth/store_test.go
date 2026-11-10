package auth

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const password = "a long enough password"

func TestLoginSessionLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want, err := s.Bootstrap("admin", password)
	if err != nil {
		t.Fatal(err)
	}
	for _, login := range [][2]string{{"admin", "wrong"}, {"missing", password}} {
		if _, token, err := s.Login(login[0], login[1]); !errors.Is(err, ErrUnauthorized) || token != "" {
			t.Fatalf("bad login %q %v", token, err)
		}
	}
	got, token, err := s.Login(" Admin ", password)
	if err != nil || got != want || len(token) != 43 {
		t.Fatalf("login: %+v %q %v", got, token, err)
	}
	if got, err := s.Session(token); err != nil || got != want {
		t.Fatalf("session: %+v %v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Session(token); err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	_, token, err = s.Login("admin", password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE sessions SET expires_at=?", time.Now().Add(-time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Session(token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
}

func TestBootstrapPersistenceAndRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "auth.db")
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
	if has, err := a.HasUsers(); err != nil || has {
		t.Fatalf("initial users: %v %v", has, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, s := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			u, e := s.Bootstrap(" Admin ", password)
			if e == nil && (u.Username != "admin" || u.Role != "admin" || u.ID == "") {
				e = errors.New("invalid user")
			}
			results <- e
		}(s)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for e := range results {
		if e == nil {
			successes++
		} else if errors.Is(e, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("bootstrap winners=%d conflicts=%d", successes, conflicts)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if has, err := a.HasUsers(); err != nil || !has {
		t.Fatalf("persisted users: %v %v", has, err)
	}
}
