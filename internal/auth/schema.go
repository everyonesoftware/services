package auth

import (
	"database/sql"
	"errors"
)

func checkSchema(tx *sql.Tx) error {
	var count, min, max int
	if err := tx.QueryRow("SELECT count(*),COALESCE(min(version),0),COALESCE(max(version),0) FROM auth_schema").Scan(&count, &min, &max); err != nil {
		return err
	}
	if count != 1 || min != 1 || max != 1 {
		return errors.New("unsupported authentication schema")
	}
	return nil
}
func (s *Store) beginWrite() (*sql.Tx, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	// Acquire the write lock before checking the version. This fences concurrent
	// schema upgrades and avoids upgrading a stale read snapshot into a writer.
	if _, err = tx.Exec("UPDATE auth_schema SET version=version"); err == nil {
		err = checkSchema(tx)
	}
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
func (s *Store) execWrite(query string, args ...any) (sql.Result, error) {
	tx, err := s.beginWrite()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(query, args...)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
