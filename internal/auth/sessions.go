package auth

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"strings"
	"time"
)

// Login always performs a password derivation, including unknown usernames.
func (s *Store) Login(username, password string) (User, string, error) {
	if len(username) > 128 || len(password) > 1024 {
		return User{}, "", ErrUnauthorized
	}
	var u User
	var salt, expected []byte
	err := s.db.QueryRow("SELECT id,username,role,salt,password_hash FROM users WHERE username=?", strings.ToLower(strings.TrimSpace(username))).Scan(&u.ID, &u.Username, &u.Role, &salt, &expected)
	if err != nil && err != sql.ErrNoRows {
		return User{}, "", err
	}
	if err == sql.ErrNoRows {
		salt = make([]byte, 16)
		expected = make([]byte, 32)
	}
	actual, e := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	if e != nil {
		return User{}, "", e
	}
	if subtle.ConstantTimeCompare(actual, expected) != 1 || err == sql.ErrNoRows {
		return User{}, "", ErrUnauthorized
	}
	token, e := randomString()
	if e != nil {
		return User{}, "", e
	}
	hash := sha256.Sum256([]byte(token))
	// Recheck the password hash in the insertion, so a concurrent password reset
	// or deletion cannot issue a valid session for an obsolete credential.
	tx, e := s.beginWrite()
	if e != nil {
		return User{}, "", e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM sessions WHERE expires_at<=?", time.Now().Unix()); e != nil {
		return User{}, "", e
	}
	result, e := tx.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at,last_used_at) SELECT ?,id,?,? FROM users WHERE id=? AND password_hash=?`, hash[:], time.Now().Add(12*time.Hour).Unix(), time.Now().Unix(), u.ID, expected)
	if e != nil {
		return User{}, "", e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return User{}, "", e
	}
	if n != 1 {
		return User{}, "", ErrUnauthorized
	}
	if e = tx.Commit(); e != nil {
		return User{}, "", e
	}
	return u, token, nil
}

func (s *Store) Session(token string) (User, error) {
	if len(token) != 43 {
		return User{}, ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(token))
	var u User
	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if err = checkSchema(tx); err != nil {
		return User{}, err
	}
	err = tx.QueryRow(`SELECT u.id,u.username,u.role FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.token_hash=? AND s.expires_at>?`, hash[:], time.Now().Unix()).Scan(&u.ID, &u.Username, &u.Role)
	if err == sql.ErrNoRows {
		return User{}, ErrUnauthorized
	}
	return u, err
}

func (s *Store) Logout(token string) error {
	hash := sha256.Sum256([]byte(token))
	_, err := s.execWrite("DELETE FROM sessions WHERE token_hash=?", hash[:])
	return err
}
