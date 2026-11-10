package publishers

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingOrWrongEncryptionKeyFailsClosed(t *testing.T) {
	for _, operation := range []string{"missing", "wrong", "truncated"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			store, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.Save(Snapshot{App: App{ID: 1, PEM: "test-private-material"}}); err != nil {
				t.Fatal(err)
			}
			store.Close()
			keyPath := filepath.Join(dir, "publishers.key")
			switch operation {
			case "missing":
				err = os.Remove(keyPath)
			case "wrong":
				err = os.WriteFile(keyPath, bytes.Repeat([]byte{1}, 32), 0600)
			case "truncated":
				err = os.WriteFile(keyPath, []byte("short"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(dir)
			if reopened != nil {
				reopened.Close()
			}
			if err == nil {
				t.Fatal("unreadable state silently replaced")
			}
		})
	}
}
func TestFutureSchemaPreventsWrites(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "publishers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("UPDATE publisher_schema SET version=999"); err != nil {
		t.Fatal(err)
	}
	if err = store.Save(Snapshot{}); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatal("future schema accepted")
	}
}
func TestCorruptEncryptedEnvelopeIsNotReset(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Save(Snapshot{}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "publishers.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE publisher_state SET envelope=x'010203'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := Open(dir)
	if reopened != nil {
		reopened.Close()
	}
	if err == nil {
		t.Fatal("corrupt state was accepted")
	}
}
