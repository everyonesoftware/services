// Package auth persists administrator credentials and scoped API keys.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrInvalid      = errors.New("invalid authentication input")
	ErrConflict     = errors.New("authentication conflict")
	ErrNotFound     = errors.New("authentication record not found")
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return nil, ErrInvalid
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	file.Close()
	if err = os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if _, err = db.Exec("PRAGMA busy_timeout=10000; PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS auth_schema (version INTEGER NOT NULL);
 INSERT INTO auth_schema(version) SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM auth_schema);`)
	if err == nil {
		err = checkSchema(tx)
	}
	if err != nil {
		tx.Rollback()
		db.Close()
		return nil, err
	}
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, role TEXT NOT NULL CHECK(role='admin'), salt BLOB NOT NULL, password_hash BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS sessions (token_hash BLOB PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, expires_at INTEGER NOT NULL, last_used_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, name TEXT NOT NULL, prefix TEXT NOT NULL, token_hash BLOB NOT NULL UNIQUE, scopes TEXT NOT NULL, repositories TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, last_used_at INTEGER NOT NULL);`)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) HasUsers() (bool, error) {
	var n int
	err := s.db.QueryRow("SELECT count(*) FROM users").Scan(&n)
	return n > 0, err
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func credentials(username, password string) (User, []byte, []byte, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" || len(username) > 128 || !utf8.ValidString(username) || !utf8.ValidString(password) || utf8.RuneCountInString(password) < 15 || len(password) > 1024 || strings.IndexFunc(username, func(r rune) bool { return r <= 32 || r == 127 }) >= 0 {
		return User{}, nil, nil, ErrInvalid
	}
	id, err := randomString()
	if err != nil {
		return User{}, nil, nil, err
	}
	salt := make([]byte, 16)
	if _, err = rand.Read(salt); err != nil {
		return User{}, nil, nil, err
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	return User{ID: id, Username: username, Role: "admin"}, salt, hash, err
}

func (s *Store) Bootstrap(username, password string) (User, error) {
	u, salt, hash, err := credentials(username, password)
	if err != nil {
		return User{}, err
	}
	result, err := s.execWrite(`INSERT INTO users(id,username,role,salt,password_hash) SELECT ?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM users)`, u.ID, u.Username, u.Role, salt, hash)
	if err != nil {
		return User{}, fmt.Errorf("bootstrap: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return User{}, err
	}
	if n != 1 {
		return User{}, ErrConflict
	}
	return u, nil
}
