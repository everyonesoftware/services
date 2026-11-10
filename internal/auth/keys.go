package auth

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

type APIKey struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Prefix       string   `json:"prefix"`
	Scopes       []string `json:"scopes"`
	Repositories []string `json:"repositories"`
	CreatedAt    string   `json:"createdAt"`
	ExpiresAt    string   `json:"expiresAt,omitempty"`
	LastUsedAt   string   `json:"lastUsedAt,omitempty"`
}

var imageRepository = regexp.MustCompile(`^ghcr\.io/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)+$`)

func (s *Store) CreateKey(name string, scopes, repositories []string, expiresAt string) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || strings.IndexFunc(name, unicode.IsControl) >= 0 || len(scopes) == 0 || len(scopes) > 2 || len(repositories) > 256 {
		return APIKey{}, "", ErrInvalid
	}
	publish := false
	seen := make(map[string]bool)
	scopeList := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if scope != "services:read" && scope != "publications:write" {
			return APIKey{}, "", ErrInvalid
		}
		if !seen[scope] {
			scopeList = append(scopeList, scope)
			seen[scope] = true
		}
		publish = publish || scope == "publications:write"
	}
	if publish && len(repositories) == 0 || !publish && len(repositories) != 0 {
		return APIKey{}, "", ErrInvalid
	}
	repos := make([]string, 0, len(repositories))
	seen = make(map[string]bool)
	for _, repo := range repositories {
		if len(repo) > 255 || !imageRepository.MatchString(repo) {
			return APIKey{}, "", ErrInvalid
		}
		if !seen[repo] {
			repos = append(repos, repo)
			seen[repo] = true
		}
	}
	sort.Strings(scopeList)
	sort.Strings(repos)
	now := time.Now().UTC()
	expiry := int64(0)
	if expiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil || parsed.Unix() <= now.Unix() {
			return APIKey{}, "", ErrInvalid
		}
		expiry = parsed.Unix()
	}
	id, err := randomString()
	if err != nil {
		return APIKey{}, "", err
	}
	random, err := randomString()
	if err != nil {
		return APIKey{}, "", err
	}
	token := "svc_" + random
	hash := sha256.Sum256([]byte(token))
	key := APIKey{ID: id, Name: name, Prefix: token[:12], Scopes: scopeList, Repositories: repos, CreatedAt: timeString(now.Unix()), ExpiresAt: timeString(expiry)}
	scopeJSON, _ := json.Marshal(scopeList)
	repoJSON, _ := json.Marshal(repos)
	_, err = s.execWrite(`INSERT INTO api_keys(id,name,prefix,token_hash,scopes,repositories,created_at,expires_at,last_used_at) VALUES(?,?,?,?,?,?,?,?,0)`, id, name, key.Prefix, hash[:], string(scopeJSON), string(repoJSON), now.Unix(), expiry)
	if err != nil {
		return APIKey{}, "", err
	}
	return key, token, nil
}

const keyColumns = "id,name,prefix,scopes,repositories,created_at,expires_at,last_used_at"

type scanner interface{ Scan(...any) error }

func scanKey(row scanner) (APIKey, error) {
	var key APIKey
	var scopes, repos string
	var created, expires, last int64
	if err := row.Scan(&key.ID, &key.Name, &key.Prefix, &scopes, &repos, &created, &expires, &last); err != nil {
		return APIKey{}, err
	}
	if err := json.Unmarshal([]byte(scopes), &key.Scopes); err != nil {
		return APIKey{}, err
	}
	if err := json.Unmarshal([]byte(repos), &key.Repositories); err != nil {
		return APIKey{}, err
	}
	key.CreatedAt = timeString(created)
	key.ExpiresAt = timeString(expires)
	key.LastUsedAt = timeString(last)
	return key, nil
}
func timeString(unix int64) string {
	if unix == 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}
func (s *Store) ListKeys() ([]APIKey, error) {
	rows, err := s.db.Query("SELECT " + keyColumns + " FROM api_keys ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]APIKey, 0)
	for rows.Next() {
		key, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}
func (s *Store) AuthenticateKey(token string) (APIKey, error) {
	if len(token) != 47 || !strings.HasPrefix(token, "svc_") {
		return APIKey{}, ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(token))
	tx, err := s.beginWrite()
	if err != nil {
		return APIKey{}, err
	}
	defer tx.Rollback()
	// Lock acquisition can wait across expiry; authorize using the time after it.
	now := time.Now().Unix()
	key, err := scanKey(tx.QueryRow("UPDATE api_keys SET last_used_at=? WHERE token_hash=? AND (expires_at=0 OR expires_at>?) RETURNING "+keyColumns, now, hash[:], now))
	if err == sql.ErrNoRows {
		return APIKey{}, ErrUnauthorized
	}
	if err != nil {
		return APIKey{}, err
	}
	if err = tx.Commit(); err != nil {
		return APIKey{}, err
	}
	return key, nil
}
func (s *Store) RevokeKey(id string) error {
	result, err := s.execWrite("DELETE FROM api_keys WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
