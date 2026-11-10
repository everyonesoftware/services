package auth

import (
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"
)

func TestFutureAuthenticationSchemaFailsClosedForOpenAndWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.Bootstrap("admin", password)
	if err != nil {
		t.Fatal(err)
	}
	_, session, err := s.Login("admin", password)
	if err != nil {
		t.Fatal(err)
	}
	key, token, err := s.CreateKey("reader", []string{"services:read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE auth_schema SET version=99"); err != nil {
		t.Fatal(err)
	}
	if other, err := Open(path); err == nil {
		other.Close()
		t.Fatal("opened future schema")
	}
	if _, err = s.Bootstrap("second", password); err == nil || errors.Is(err, ErrConflict) {
		t.Fatalf("old bootstrap writes=%v", err)
	}
	if _, err = s.CreateUser("second", password); err == nil {
		t.Fatal("old create user writes")
	}
	if err = s.ChangePassword(user.ID, "replacement long password"); err == nil {
		t.Fatal("old password writes")
	}
	if _, _, err = s.CreateKey("other", []string{"services:read"}, nil, ""); err == nil {
		t.Fatal("old create key writes")
	}
	if err = s.RevokeKey(key.ID); err == nil {
		t.Fatal("old revoke key writes")
	}
	if _, err = s.AuthenticateKey(token); err == nil {
		t.Fatal("old key authenticates")
	}
	if _, err = s.Session(session); err == nil {
		t.Fatal("old session authenticates")
	}
	if err = s.Logout(session); err == nil {
		t.Fatal("old logout writes")
	}
}
func TestPasswordsAndSessionsAreStoredAsHashes(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.Bootstrap("admin", password)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Login("admin", password)
	if err != nil {
		t.Fatal(err)
	}
	var hash, salt []byte
	if err = s.db.QueryRow("SELECT password_hash,salt FROM users WHERE id=?", user.ID).Scan(&hash, &salt); err != nil {
		t.Fatal(err)
	}
	if len(hash) != 32 || len(salt) != 16 || string(hash) == password {
		t.Fatal("password not hashed/salted")
	}
	if err = s.db.QueryRow("SELECT token_hash FROM sessions").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256([]byte(token))
	if string(hash) != string(expected[:]) {
		t.Fatal("session not hashed")
	}
}
