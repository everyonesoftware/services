package operations

import (
	"path/filepath"
	"testing"
)

func TestExistingWorkerRefusesWritesAfterSchemaVersionChanges(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "ops.db"))
	defer s.Close()
	o, _ := s.Submit("a", "update project", "", "")
	s.Claim()
	if _, err := s.db.Exec(`PRAGMA user_version=99`); err != nil {
		t.Fatal(err)
	}
	if err := s.Message(o.ID, "unsafe old worker write"); err == nil {
		t.Fatal("old worker wrote into incompatible schema")
	}
}
