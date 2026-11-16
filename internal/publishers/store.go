// Package publishers stores private push-integration configuration. It never polls
// GitHub for publications or authorizes Docker operations.
package publishers

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

type App struct {
	ID              int64
	Slug, Name, PEM string
	// Permission metadata is a last-checked hint for the UI, never authorization.
	PermissionsChecked bool
	VariablesWrite     bool
	SettingsURL        string
}
type Tailnet struct {
	ClientID string `json:"clientId"`
	Audience string `json:"audience"`
	Tags     string `json:"tags"`
}
type Repository struct {
	ID             int64  `json:"id"`
	FullName       string `json:"fullName"`
	DefaultBranch  string `json:"defaultBranch"`
	InstallationID int64  `json:"installationId"`
}

// Deletion stores private progress and the exact reviewed manual attestation.
type Deletion struct {
	PRURL              string
	WorkflowVerified   bool
	TailscaleConfirmed bool
	// Historical attestation remains true even when its current deletion proof expires.
	TailscaleRevocationAttested bool
	// Approval binds the manual attestation to the exact saved tailnet and scope.
	TailscaleApproval string
}

// Connection is a private record. Never serialize it directly to an API response.
type Connection struct {
	ID                   string
	Repository           Repository
	ImageRepository      string
	State                string
	Error                string
	KeyID                string
	Secret               string
	PreviousKeyID        string
	PreviousSecret       string
	LastPublication      *PublicationReceipt
	CreatedAt            string
	VerifiedAt           string
	ChallengeHash        string
	ChallengeExpires     int64
	ChallengeUsed        bool
	RunID                string
	Configured           bool
	RemoteDisablePending bool
	Deletion             *Deletion
	// Receipt ordering belongs to the private connection, not its public JSON view.
	LastPublicationRunID      string
	LastPublicationRunAttempt int
}

type PublicationReceipt struct {
	Version     string   `json:"version,omitempty"`
	Digest      string   `json:"digest"`
	Tags        []string `json:"tags"`
	Commit      string   `json:"commit"`
	RunURL      string   `json:"runUrl"`
	PublishedAt string   `json:"publishedAt"`
	ReceivedAt  string   `json:"receivedAt"`
	Result      string   `json:"result"`
}
type Pending struct {
	Hash    string
	UserID  string
	Expires int64
}
type Snapshot struct {
	App         App
	Tailnet     Tailnet
	Tailnets    map[string]Tailnet
	Connections []Connection
	Pending     Pending
}
type Store struct {
	db   *sql.DB
	aead cipher.AEAD
	mu   sync.Mutex
}

func Open(directory string) (*Store, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("publisher storage requires an absolute persistent directory")
	}
	if err := checkStorageDirectories(directory); err != nil {
		return nil, err
	}
	var createdDirectories []string
	for path := filepath.Clean(directory); ; path = filepath.Dir(path) {
		_, err := os.Lstat(path)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		createdDirectories = append(createdDirectories, path)
		if filepath.Dir(path) == path {
			break
		}
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if err := checkStorageDirectories(directory); err != nil {
		return nil, err
	}
	// The data mount is operator-owned and shared with other Services stores.
	// A group-writable bind may permit file creation but not chmod. MkdirAll
	// protects newly created directories; preserve existing mount permissions.
	// Persist every newly created directory entry, not just the leaf. Unchanged
	// ancestors need only traversal permission, not permission to open for fsync.
	for _, path := range createdDirectories {
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return nil, err
		}
	}
	dbPath := filepath.Join(directory, "publishers.db")
	keyPath := filepath.Join(directory, "publishers.key")
	for _, path := range []string{dbPath, keyPath, dbPath + "-journal", dbPath + "-wal", dbPath + "-shm"} {
		if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
			return nil, errors.New("publisher storage must use regular files")
		} else if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	// Bound the key read even if a damaged file is unexpectedly large.
	keyFile, err := os.Open(keyPath)
	var key []byte
	if err == nil {
		key, err = io.ReadAll(io.LimitReader(keyFile, 33))
		err = errors.Join(err, keyFile.Close())
	}
	if os.IsNotExist(err) {
		if info, e := os.Stat(dbPath); e == nil && info.Size() > 0 {
			return nil, errors.New("publisher encryption key missing; restore the key with its database")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		_, err = f.Write(key)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("invalid publisher encryption key; restore a matching backup")
	}
	if err = os.Chmod(keyPath, 0600); err != nil {
		return nil, err
	}
	// The key's directory entry must be durable before encrypted state exists.
	if err = syncDirectory(directory); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	newDatabase := true
	if info, statErr := os.Stat(dbPath); statErr == nil {
		newDatabase = info.Size() == 0
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = os.Chmod(dbPath, 0600); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	good := false
	defer func() {
		if !good {
			db.Close()
		}
	}()
	if _, err = db.Exec("PRAGMA busy_timeout=10000; PRAGMA synchronous=FULL;"); err != nil {
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if newDatabase {
		if _, err = tx.Exec(`CREATE TABLE publisher_schema(version INTEGER NOT NULL); INSERT INTO publisher_schema VALUES(1); CREATE TABLE publisher_state(id INTEGER PRIMARY KEY CHECK(id=1), envelope BLOB NOT NULL);`); err != nil {
			return nil, err
		}
	}
	var version, count int
	if err = tx.QueryRow("SELECT count(*),coalesce(min(version),0) FROM publisher_schema").Scan(&count, &version); err != nil {
		return nil, err
	}
	if count != 1 || version != 1 {
		return nil, errors.New("unsupported publisher storage schema")
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}
	store := &Store{db: db, aead: aead}
	if _, err = store.Load(); err != nil {
		return nil, err
	}
	if err = syncDirectory(directory); err != nil {
		return nil, err
	}
	good = true
	return store, nil
}
func (s *Store) Close() error            { return s.db.Close() }
func (s *Store) Load() (Snapshot, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.load() }
func (s *Store) load() (Snapshot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	var version, count int
	if err = tx.QueryRow("SELECT count(*),coalesce(min(version),0) FROM publisher_schema").Scan(&count, &version); err != nil {
		return Snapshot{}, err
	}
	if count != 1 || version != 1 {
		return Snapshot{}, errors.New("unsupported publisher storage schema")
	}
	var size int
	var storageType string
	err = tx.QueryRow("SELECT typeof(envelope),length(envelope) FROM publisher_state WHERE id=1").Scan(&storageType, &size)
	if err == sql.ErrNoRows {
		return Snapshot{Connections: []Connection{}}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	if storageType != "blob" {
		return Snapshot{}, errors.New("invalid publisher state envelope storage type")
	}
	if size > (4<<20)+1+s.aead.NonceSize()+s.aead.Overhead() {
		return Snapshot{}, errors.New("publisher state exceeds storage limit")
	}
	var data []byte
	err = tx.QueryRow("SELECT envelope FROM publisher_state WHERE id=1").Scan(&data)
	if err == sql.ErrNoRows {
		return Snapshot{Connections: []Connection{}}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	n := s.aead.NonceSize()
	if len(data) < 1+n+s.aead.Overhead() || data[0] != 1 {
		return Snapshot{}, errors.New("invalid publisher state envelope")
	}
	plain, err := s.aead.Open(nil, data[1:1+n], data[1+n:], []byte("services-publishers-v1"))
	if err != nil {
		return Snapshot{}, errors.New("publisher state authentication failed; restore the matching encryption key")
	}
	var state *Snapshot
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&state); err != nil || state == nil {
		return Snapshot{}, errors.New("invalid publisher state")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Snapshot{}, errors.New("invalid publisher state")
	}
	if state.Connections == nil {
		state.Connections = []Connection{}
	}
	return *state, nil
}
func (s *Store) Save(state Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plain, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if len(plain) > 4<<20 {
		return errors.New("publisher state exceeds storage limit")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	data := append([]byte{1}, nonce...)
	data = s.aead.Seal(data, nonce, plain, []byte("services-publishers-v1"))
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version, count int
	if err = tx.QueryRow("SELECT count(*),coalesce(min(version),0) FROM publisher_schema").Scan(&count, &version); err != nil {
		return err
	}
	if count != 1 || version != 1 {
		return errors.New("unsupported publisher storage schema")
	}
	if _, err = tx.Exec("INSERT INTO publisher_state(id,envelope) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET envelope=excluded.envelope", data); err != nil {
		return err
	}
	return tx.Commit()
}
func RandomID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate publisher identifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Refuse linked storage roots and ancestors before creating files. Like the
// repository's other private stores, this assumes operator-owned parent paths;
// it does not protect against an administrator replacing paths concurrently.
func checkStorageDirectories(directory string) error {
	for path := filepath.Clean(directory); ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err == nil && !info.IsDir() {
			return errors.New("publisher storage requires unlinked directories")
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(path) == path {
			return nil
		}
	}
}
