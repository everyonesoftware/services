package updates

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// observe only commits a registry result while its original publication still
// owns the tag. SQLite evaluates the condition and upsert in one writer statement.
func (s *PublicationStore) observe(ctx context.Context, repository, tag string, expectedID int64, digest string, checkedAt time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// As in accept, acquire the writer lock before checking compatibility so a
	// concurrent migration cannot race an already-open coordinator's write.
	if _, err = tx.ExecContext(ctx, "UPDATE publication_guard SET id=id WHERE id=1"); err != nil {
		return false, err
	}
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return false, err
	}
	if version != 1 {
		return false, errors.New("unsupported publication schema")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO publication_observations(repository,tag,event_id,digest,checked_at)
 SELECT repository,tag,event_id,?,? FROM publication_tags WHERE repository=? AND tag=? AND event_id=?
 ON CONFLICT(repository,tag) DO UPDATE SET event_id=excluded.event_id,digest=excluded.digest,checked_at=excluded.checked_at`,
		digest, checkedAt.UTC().Format(time.RFC3339Nano), repository, tag, expectedID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return n > 0, nil
}

// loadObservation never applies a prior event's reconciliation to a new receipt.
func (s *PublicationStore) loadObservation(ctx context.Context, repository, tag string, eventID int64) (string, time.Time, error) {
	var digest, checked string
	err := s.db.QueryRowContext(ctx, `SELECT digest,checked_at FROM publication_observations WHERE repository=? AND tag=? AND event_id=?`, repository, tag, eventID).Scan(&digest, &checked)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	if err != nil {
		return "", time.Time{}, err
	}
	at, err := time.Parse(time.RFC3339Nano, checked)
	return digest, at, err
}

// initReconciliations installs the additive observation table after the base schema.
func (s *PublicationStore) initReconciliations() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS publication_observations (
 repository TEXT NOT NULL, tag TEXT NOT NULL, event_id INTEGER NOT NULL,
 digest TEXT NOT NULL, checked_at TEXT NOT NULL,
 PRIMARY KEY(repository,tag),
 FOREIGN KEY(repository,tag) REFERENCES publication_tags(repository,tag) ON DELETE CASCADE)`)
	return err
}
