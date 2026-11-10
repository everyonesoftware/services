package publishers

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadChecksSchemaAfterOpen(t *testing.T) {
	for _, mutation := range []string{"UPDATE publisher_schema SET version=999", "DELETE FROM publisher_schema", "INSERT INTO publisher_schema VALUES(1)"} {
		t.Run(mutation, func(t *testing.T) {
			store, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.Save(Snapshot{App: App{PEM: "fixture-private-material"}}); err != nil {
				t.Fatal(err)
			}
			if _, err = store.db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if _, err = store.Load(); err == nil || !strings.Contains(err.Error(), "schema") {
				t.Fatal("Load accepted changed schema")
			}
		})
	}
}

func TestReopenDoesNotRepairMissingSchemaOrState(t *testing.T) {
	for _, mutation := range []string{"DELETE FROM publisher_schema", "DROP TABLE publisher_schema", "DROP TABLE publisher_state"} {
		t.Run(mutation, func(t *testing.T) {
			dir := t.TempDir()
			store, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.Save(Snapshot{}); err != nil {
				t.Fatal(err)
			}
			if _, err = store.db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			store.Close()
			reopened, err := Open(dir)
			if reopened != nil {
				reopened.Close()
			}
			if err == nil {
				t.Fatal("repaired damaged storage")
			}
		})
	}
}

func TestLoadRejectsNonBlobEnvelope(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Save(Snapshot{}); err != nil {
		t.Fatal(err)
	}
	var data []byte
	if err = store.db.QueryRow("SELECT envelope FROM publisher_state WHERE id=1").Scan(&data); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec("UPDATE publisher_state SET envelope=?", string(data)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(); err == nil {
		t.Fatal("accepted TEXT envelope with byte-size validation bypass")
	}
}

func TestLoadRejectsOversizedEnvelopeBeforeDecrypting(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.db.Exec("INSERT INTO publisher_state(id,envelope) VALUES(1,zeroblob(?))", (4<<20)+30); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Load(); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected size limit, got %v", err)
	}
}

func TestLoadRejectsInvalidAuthenticatedJSON(t *testing.T) {
	for _, plain := range []string{"null", `{"FutureField":"must-not-be-discarded"}`} {
		t.Run(plain, func(t *testing.T) {
			store, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			nonce := bytes.Repeat([]byte{3}, store.aead.NonceSize())
			data := store.aead.Seal(append([]byte{1}, nonce...), nonce, []byte(plain), []byte("services-publishers-v1"))
			if _, err = store.db.Exec("INSERT INTO publisher_state(id,envelope) VALUES(1,?)", data); err != nil {
				t.Fatal(err)
			}
			if _, err = store.Load(); err == nil {
				t.Fatal("accepted invalid or unknown state")
			}
		})
	}
}

func TestOversizedKeyRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "publishers.key"), bytes.Repeat([]byte{1}, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if store != nil {
		store.Close()
	}
	if err == nil {
		t.Fatal("oversized key accepted")
	}
}
