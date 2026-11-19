package updates

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"services/internal/groups"
)

func TestPublicationBindingFinanceLegacyMigrationPreservesHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "publications.db")
	s, err := OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	read := func(name string) groups.ImagePublication {
		t.Helper()
		raw, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var p groups.ImagePublication
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	old := read("finance-publication-109.json")
	next := read("finance-publication-117.json")
	if got, err := s.accept(ctx, old); err != nil || got != "accepted" {
		t.Fatalf("109: %s %v", got, err)
	}
	// Simulate an existing pre-identity schema and preserve its original payload bytes.
	raw, _ := os.ReadFile("testdata/finance-publication-109.json")
	if _, err := s.db.Exec("UPDATE publication_events SET payload=?", string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DROP TABLE publication_repository_identities"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenPublicationStore(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.PublicationRepositoryBinding(ctx, old.Repository)
	if err != nil || legacy.SourceRepository != old.SourceRepository || legacy.RepositoryID != 0 || legacy.InstallationID != 0 || legacy.Revision != 0 {
		t.Fatalf("legacy: %+v %v", legacy, err)
	}
	if _, err := s.accept(ctx, next); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("117 before review: %v", err)
	}
	// A populated floor must survive reconciliation without reset or rename.
	if _, err := s.db.Exec("INSERT INTO publication_replay_floors(repository,run_id,attempt) VALUES(?,?,?)", old.Repository, "100", 1); err != nil {
		t.Fatal(err)
	}
	snapshot := func() []string {
		t.Helper()
		var rowsOut []string
		for _, query := range []string{
			`SELECT json_array(id,repository,source,run_id,attempt,payload,received_at) FROM publication_events ORDER BY id`,
			`SELECT json_array(repository,tag,event_id) FROM publication_tags ORDER BY repository,tag`,
			`SELECT json_array(repository,run_id,attempt) FROM publication_replay_floors ORDER BY repository`,
		} {
			rows, err := s.db.Query(query)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var value string
				if err := rows.Scan(&value); err != nil {
					t.Fatal(err)
				}
				rowsOut = append(rowsOut, value)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
		return rowsOut
	}
	before := snapshot()
	// IDs here are locally approved test identities, not claims from the fixtures.
	if err := s.ReconcilePublicationRepository(ctx, legacy, 42, 7, next.SourceRepository); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("reconciliation changed history:\n%v\n%v", before, after)
	}
	if got, err := s.accept(ctx, next); err != nil || got != "accepted" {
		t.Fatalf("117 after review: %s %v", got, err)
	}
	var retained string
	if err := s.db.QueryRow("SELECT payload FROM publication_events WHERE run_id=?", old.RunID).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained != string(raw) {
		t.Fatal("original 109 payload bytes changed")
	}
}

func TestPublicationBindingPayloadClaimNeverEstablishesOwnership(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	p.SourceRepositoryID = "42"
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	legacy, _ := s.PublicationRepositoryBinding(ctx, p.Repository)
	if legacy.RepositoryID != 0 || legacy.InstallationID != 0 || legacy.Revision != 0 {
		t.Fatalf("claim trusted: %+v", legacy)
	}
	if err := s.ReconcilePublicationRepository(ctx, legacy, 42, 7, p.SourceRepository); err != nil {
		t.Fatal(err)
	}
	p.RunID = "124"
	p.RunURL = "https://github.com/" + p.SourceRepository + "/actions/runs/" + p.RunID
	p.PublishedAt = "2026-11-09T13:00:00Z"
	p.SourceRepositoryID = "43"
	if _, err := s.accept(ctx, p); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("mismatched identity claim accepted: %v", err)
	}
	p.SourceRepositoryID = ""
	if got, err := s.accept(ctx, p); err != nil || got != "accepted" {
		t.Fatalf("legacy payload on verified binding: %s %v", got, err)
	}
}

func TestPublicationBindingRenameCannotReuseRunAttempt(t *testing.T) {
	s := openPublicationTestStore(t)
	ctx := context.Background()
	p := publicationFixture()
	if _, err := s.accept(ctx, p); err != nil {
		t.Fatal(err)
	}
	legacy, _ := s.PublicationRepositoryBinding(ctx, p.Repository)
	if err := s.ReconcilePublicationRepository(ctx, legacy, 42, 7, "acme/renamed"); err != nil {
		t.Fatal(err)
	}
	p.SourceRepository = "acme/renamed"
	p.RunURL = "https://github.com/" + p.SourceRepository + "/actions/runs/" + p.RunID
	// Even a valid renamed URL/source and a new tag cannot reuse the old identity.
	p.Tags = []string{"new-tag"}
	p.PublishedAt = "2026-11-09T13:00:00Z"
	if got, err := s.accept(ctx, p); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("renamed replay accepted: %s %v", got, err)
	}
}
