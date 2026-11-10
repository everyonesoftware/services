package auth

import (
	"database/sql"
	"errors"

	"modernc.org/sqlite"
)

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query("SELECT id,username,role FROM users ORDER BY username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var u User
		if err = rows.Scan(&u.ID, &u.Username, &u.Role); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) CreateUser(username, password string) (User, error) {
	u, salt, hash, err := credentials(username, password)
	if err != nil {
		return User{}, err
	}
	_, err = s.execWrite("INSERT INTO users(id,username,role,salt,password_hash) VALUES(?,?,?,?,?)", u.ID, u.Username, u.Role, salt, hash)
	if isUnique(err) {
		return User{}, ErrConflict
	}
	if err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) DeleteUser(id string) error {
	// The conditional delete is a single write statement: concurrent processes
	// cannot each read a count of two and then remove both administrators.
	result, err := s.execWrite("DELETE FROM users WHERE id=? AND (SELECT count(*) FROM users)>1", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	var exists int
	err = s.db.QueryRow("SELECT 1 FROM users WHERE id=?", id).Scan(&exists)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrConflict
}

func (s *Store) ChangePassword(id, password string) error {
	_, salt, hash, err := credentials("password-reset", password)
	if err != nil {
		return err
	}
	tx, err := s.beginWrite()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE users SET salt=?,password_hash=? WHERE id=?", salt, hash, id)
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
	if _, err = tx.Exec("DELETE FROM sessions WHERE user_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func isUnique(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && (e.Code() == 1555 || e.Code() == 2067)
}
