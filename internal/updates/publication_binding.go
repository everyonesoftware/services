package updates

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// PublicationBinding separates verified immutable ownership from the current
// provider name. A legacy source has zero identity and revision until explicitly
// upgraded by a caller that verifies the provider identity or obtains approval.
type PublicationBinding struct {
	ImageRepository  string `json:"imageRepository"`
	SourceRepository string `json:"sourceRepository"`
	RepositoryID     int64  `json:"repositoryId"`
	InstallationID   int64  `json:"installationId"`
	Revision         int64  `json:"revision"`
}

type bindingQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func publicationRepositoryBinding(ctx context.Context, q bindingQuerier, image string) (PublicationBinding, error) {
	b := PublicationBinding{ImageRepository: image}
	err := q.QueryRowContext(ctx, `SELECT r.source,COALESCE(i.repository_id,0),COALESCE(i.installation_id,0),COALESCE(i.revision,0)
 FROM publication_repositories r LEFT JOIN publication_repository_identities i ON i.repository=r.repository WHERE r.repository=?`, image).Scan(&b.SourceRepository, &b.RepositoryID, &b.InstallationID, &b.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	return b, err
}

// PublicationRepositoryBinding reads committed ownership, including legacy names.
func (s *PublicationStore) PublicationRepositoryBinding(ctx context.Context, image string) (PublicationBinding, error) {
	return publicationRepositoryBinding(ctx, s.db, image)
}

// ReconcilePublicationRepository reserves or refreshes provenance ownership. The
// caller must independently verify both provider IDs and currentSource; payload
// claims are never sufficient authority. History and tag/replay state are untouched.
func (s *PublicationStore) ReconcilePublicationRepository(ctx context.Context, expected PublicationBinding, repositoryID, installationID int64, currentSource string) error {
	ref, err := parseReference(expected.ImageRepository)
	if err != nil || len(expected.ImageRepository) > 255 || !strings.HasPrefix(expected.ImageRepository, "ghcr.io/") || ref.canonicalRepository() != expected.ImageRepository || !strings.Contains(ref.repository, "/") || !publicationSource.MatchString(currentSource) || strings.HasSuffix(currentSource, "/.") || strings.HasSuffix(currentSource, "/..") || repositoryID <= 0 || installationID <= 0 {
		return ErrPublicationConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE publication_guard SET id=id WHERE id=1"); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 1 {
		return errors.New("unsupported publication schema")
	}
	current, err := publicationRepositoryBinding(ctx, tx, expected.ImageRepository)
	if err != nil {
		return err
	}
	if current.RepositoryID == repositoryID && current.InstallationID == installationID && current.SourceRepository == currentSource && current.Revision > 0 {
		return nil
	}
	if current != expected || (current.RepositoryID != 0 && (current.RepositoryID != repositoryID || current.InstallationID != installationID)) {
		return ErrPublicationConflict
	}
	if current.SourceRepository == "" {
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM publication_repositories").Scan(&count); err != nil {
			return err
		}
		if count >= maxPublicationRepositories {
			return errors.New("publication repository capacity reached")
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO publication_repositories(repository,source) VALUES(?,?)", expected.ImageRepository, currentSource); err != nil {
			return err
		}
	} else {
		if _, err = tx.ExecContext(ctx, "UPDATE publication_repositories SET source=? WHERE repository=?", currentSource, expected.ImageRepository); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO publication_repository_identities(repository,repository_id,installation_id,revision) VALUES(?,?,?,?)
 ON CONFLICT(repository) DO UPDATE SET revision=excluded.revision`, expected.ImageRepository, repositoryID, installationID, current.Revision+1)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// PublicationRepositoryBinding forwards committed provenance reads to the store.
func (c *Checker) PublicationRepositoryBinding(ctx context.Context, image string) (PublicationBinding, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.publicationStore == nil {
		return PublicationBinding{}, errors.New("publication store unavailable")
	}
	return c.publicationStore.PublicationRepositoryBinding(ctx, image)
}

// ReconcilePublicationRepository forwards independently authorized ownership CAS.
func (c *Checker) ReconcilePublicationRepository(ctx context.Context, expected PublicationBinding, repositoryID, installationID int64, currentSource string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.publicationStore == nil {
		return errors.New("publication store unavailable")
	}
	return c.publicationStore.ReconcilePublicationRepository(ctx, expected, repositoryID, installationID, currentSource)
}

func (s *PublicationStore) initRepositoryBindings() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS publication_repository_identities (
 repository TEXT PRIMARY KEY REFERENCES publication_repositories(repository),
 repository_id INTEGER NOT NULL, installation_id INTEGER NOT NULL, revision INTEGER NOT NULL)`)
	return err
}
