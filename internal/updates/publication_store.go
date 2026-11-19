package updates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
	"services/internal/groups"
)

// ErrPublicationConflict identifies changed replay payloads and repository/source
// ownership conflicts. Callers can use errors.Is without exposing DB diagnostics.
var ErrPublicationConflict = errors.New("image publication conflicts with accepted provenance")

// Storage is bounded to 128 source-bound repositories and the 256 most recently
// updated tag mappings per repository. Evict by event ID, then tag, oldest first;
// all tags in an incoming publication (at most 64) remain retained. Evicted tags
// can fall back to registry checks. Keep the most recent 1024 replay identities
// plus any older event still referenced by a current tag (at most 1280
// events/repository). Freshness is evaluated independently for each tag.
// Publication time is primary; run creation order only breaks equal timestamps.
// A compact replay floor remembers the greatest pruned run/attempt identity:
// identities at/below that floor are stale even with changed payload/timestamps.
// Limitation: rerunning an older workflow below the replay floor is rejected;
// trigger a newly-created workflow run instead. This bounds replay state without
// silently allowing an evicted identity to be reused. Ordinary new publishing
// never exhausts a lifetime event quota.
const maxPublicationRepositories = 128
const maxPublicationTags = 256
const maxPublicationEvents = 1024

// PublicationStore owns a dedicated publications.db, never the operations schema.
// Every accepted event (including its receipt time) and all tags commit atomically.
type PublicationStore struct{ db *sql.DB }
type storedPublication struct {
	ID          int64
	Publication groups.ImagePublication
	ReceivedAt  time.Time
}

func OpenPublicationStore(path string) (*PublicationStore, error) {
	if path == "" || strings.EqualFold(filepath.Base(path), "operations.db") {
		return nil, errors.New("a separate publication database path is required")
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := u.Query()
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*PublicationStore, error) { db.Close(); return nil, err }
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version > 1 {
		return fail(fmt.Errorf("unsupported publication schema %d", version))
	}
	_, err = db.Exec(`BEGIN IMMEDIATE;
 CREATE TABLE IF NOT EXISTS publication_guard (id INTEGER PRIMARY KEY CHECK(id=1));
 INSERT OR IGNORE INTO publication_guard VALUES(1);
 CREATE TABLE IF NOT EXISTS publication_repositories (repository TEXT PRIMARY KEY, source TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS publication_replay_floors (repository TEXT PRIMARY KEY, run_id TEXT NOT NULL, attempt INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS publication_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, repository TEXT NOT NULL, source TEXT NOT NULL,
 run_id TEXT NOT NULL, attempt INTEGER NOT NULL, payload TEXT NOT NULL, received_at TEXT NOT NULL,
 UNIQUE(repository,source,run_id,attempt));
 CREATE TABLE IF NOT EXISTS publication_tags (
 repository TEXT NOT NULL, tag TEXT NOT NULL, event_id INTEGER NOT NULL REFERENCES publication_events(id),
 PRIMARY KEY(repository,tag));
 PRAGMA user_version=1;
 COMMIT;`)
	if err != nil {
		return fail(err)
	}
	s := &PublicationStore{db: db}
	if err = s.initReconciliations(); err != nil {
		return fail(err)
	}
	if err = s.initRepositoryBindings(); err != nil {
		return fail(err)
	}
	return s, nil
}
func (s *PublicationStore) Close() error { return s.db.Close() }

func (s *PublicationStore) accept(ctx context.Context, p groups.ImagePublication) (string, error) {
	if err := ValidatePublication(p); err != nil {
		return "", err
	}
	p = copyPublication(p)
	published, _ := time.Parse(time.RFC3339Nano, p.PublishedAt)
	if published.After(time.Now().Add(5 * time.Minute)) {
		return "", errors.New("publication timestamp exceeds permitted five-minute clock skew")
	}
	// Tag order has no semantic meaning for an atomic publication.
	sort.Strings(p.Tags)
	payload, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// Take SQLite's writer lock before reading, including across store instances.
	if _, err = tx.ExecContext(ctx, "UPDATE publication_guard SET id=id WHERE id=1"); err != nil {
		return "", err
	}
	var version int
	if err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return "", err
	}
	if version != 1 {
		return "", errors.New("unsupported publication schema")
	}
	var source string
	err = tx.QueryRowContext(ctx, "SELECT source FROM publication_repositories WHERE repository=?", p.Repository).Scan(&source)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	newRepository := errors.Is(err, sql.ErrNoRows)
	if !newRepository && !strings.EqualFold(source, p.SourceRepository) {
		return "", ErrPublicationConflict
	}
	if p.SourceRepositoryID != "" {
		binding, err := publicationRepositoryBinding(ctx, tx, p.Repository)
		if err != nil {
			return "", err
		}
		if binding.RepositoryID != 0 && p.SourceRepositoryID != strconv.FormatInt(binding.RepositoryID, 10) {
			return "", ErrPublicationConflict
		}
	}
	var previous string
	// Run/attempt identity belongs to the image's immutable ownership, not
	// its current name. Retained events under previous names still fence replay.
	err = tx.QueryRowContext(ctx, "SELECT payload FROM publication_events WHERE repository=? AND run_id=? AND attempt=? ORDER BY id LIMIT 1", p.Repository, p.RunID, p.RunAttempt).Scan(&previous)
	if err == nil {
		if previous != string(payload) {
			return "", ErrPublicationConflict
		}
		return "duplicate", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var floorRun string
	var floorAttempt int
	err = tx.QueryRowContext(ctx, "SELECT run_id,attempt FROM publication_replay_floors WHERE repository=?", p.Repository).Scan(&floorRun, &floorAttempt)
	if err == nil {
		if comparePublicationSequence(p.RunID, p.RunAttempt, floorRun, floorAttempt) <= 0 {
			return "stale", nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	// Freshness belongs to each tag, not to the repository: a delayed event
	// may still advance tags untouched by newer publications. The replay floor
	// above independently prevents pruned identities from being reused.
	var tagsToApply []string
	for _, tag := range p.Tags {
		var oldPayload string
		err = tx.QueryRowContext(ctx, `SELECT e.payload FROM publication_tags t JOIN publication_events e ON e.id=t.event_id WHERE t.repository=? AND t.tag=?`, p.Repository, tag).Scan(&oldPayload)
		if errors.Is(err, sql.ErrNoRows) {
			tagsToApply = append(tagsToApply, tag)
			continue
		}
		if err != nil {
			return "", err
		}
		var old groups.ImagePublication
		if err = json.Unmarshal([]byte(oldPayload), &old); err != nil {
			return "", err
		}
		if publicationIsStale(p, old) {
			continue
		}
		tagsToApply = append(tagsToApply, tag)
	}
	if len(tagsToApply) == 0 {
		return "stale", nil
	}
	var count int
	if newRepository {
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM publication_repositories").Scan(&count); err != nil {
			return "", err
		}
		if count >= maxPublicationRepositories {
			return "", errors.New("publication repository capacity reached")
		}
	}
	if newRepository {
		if _, err = tx.ExecContext(ctx, "INSERT INTO publication_repositories(repository,source) VALUES(?,?)", p.Repository, p.SourceRepository); err != nil {
			return "", err
		}
	}
	inserted, err := tx.ExecContext(ctx, "INSERT INTO publication_events(repository,source,run_id,attempt,payload,received_at) VALUES(?,?,?,?,?,?)", p.Repository, strings.ToLower(p.SourceRepository), p.RunID, p.RunAttempt, string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", err
	}
	id, err := inserted.LastInsertId()
	if err != nil {
		return "", err
	}
	for _, tag := range tagsToApply {
		if _, err = tx.ExecContext(ctx, `INSERT INTO publication_tags(repository,tag,event_id) VALUES(?,?,?) ON CONFLICT(repository,tag) DO UPDATE SET event_id=excluded.event_id`, p.Repository, tag, id); err != nil {
			return "", err
		}
	}
	// Protect all incoming tags, including mappings already newer than this
	// event. Otherwise retain mappings by update order, breaking ties by tag.
	retentionArgs := []any{p.Repository, p.Repository}
	for _, tag := range p.Tags {
		retentionArgs = append(retentionArgs, tag)
	}
	retentionArgs = append(retentionArgs, maxPublicationTags)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(p.Tags)), ",")
	if _, err = tx.ExecContext(ctx, `DELETE FROM publication_tags WHERE repository=?
 AND tag NOT IN (SELECT tag FROM publication_tags WHERE repository=?
 ORDER BY (tag IN (`+placeholders+`)) DESC,event_id DESC,tag DESC LIMIT ?)`, retentionArgs...); err != nil {
		return "", err
	}
	var retiredRun string
	var retiredAttempt int
	err = tx.QueryRowContext(ctx, `SELECT run_id,attempt FROM publication_events WHERE repository=?
 AND id NOT IN (SELECT event_id FROM publication_tags WHERE repository=?)
 AND id NOT IN (SELECT id FROM publication_events WHERE repository=? ORDER BY id DESC LIMIT ?)
 ORDER BY length(run_id) DESC,run_id DESC,attempt DESC LIMIT 1`, p.Repository, p.Repository, p.Repository, maxPublicationEvents).Scan(&retiredRun, &retiredAttempt)
	if err == nil {
		if comparePublicationSequence(retiredRun, retiredAttempt, floorRun, floorAttempt) > 0 {
			if _, err = tx.ExecContext(ctx, `INSERT INTO publication_replay_floors(repository,run_id,attempt) VALUES(?,?,?) ON CONFLICT(repository) DO UPDATE SET run_id=excluded.run_id,attempt=excluded.attempt`, p.Repository, retiredRun, retiredAttempt); err != nil {
				return "", err
			}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM publication_events WHERE repository=?
 AND id NOT IN (SELECT event_id FROM publication_tags WHERE repository=?)
 AND id NOT IN (SELECT id FROM publication_events WHERE repository=? ORDER BY id DESC LIMIT ?)`, p.Repository, p.Repository, p.Repository, maxPublicationEvents); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return "accepted", nil
}
func publicationIsStale(p, old groups.ImagePublication) bool {
	published, _ := time.Parse(time.RFC3339Nano, p.PublishedAt)
	previous, _ := time.Parse(time.RFC3339Nano, old.PublishedAt)
	if published.Before(previous) {
		return true
	}
	if published.After(previous) {
		return false
	}
	// Run creation order is only a deterministic tie-breaker for equal publish
	// timestamps. A rerun of an earlier-created workflow can publish later.
	return comparePublicationSequence(p.RunID, p.RunAttempt, old.RunID, old.RunAttempt) <= 0
}
func comparePublicationSequence(run string, attempt int, previousRun string, previousAttempt int) int {
	if len(run) < len(previousRun) {
		return -1
	}
	if len(run) > len(previousRun) {
		return 1
	}
	if run < previousRun {
		return -1
	}
	if run > previousRun {
		return 1
	}
	if attempt < previousAttempt {
		return -1
	}
	if attempt > previousAttempt {
		return 1
	}
	return 0
}
func (s *PublicationStore) current(ctx context.Context, repository, tag string) (*storedPublication, error) {
	var r storedPublication
	var payload, received string
	err := s.db.QueryRowContext(ctx, `SELECT e.id,e.payload,e.received_at FROM publication_tags t JOIN publication_events e ON e.id=t.event_id WHERE t.repository=? AND t.tag=?`, repository, tag).Scan(&r.ID, &payload, &received)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(payload), &r.Publication); err != nil {
		return nil, err
	}
	if err = ValidatePublication(r.Publication); err != nil {
		return nil, err
	}
	r.ReceivedAt, err = time.Parse(time.RFC3339Nano, received)
	if err != nil {
		return nil, err
	}
	return &r, nil
}
