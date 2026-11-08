// Package operations owns the durable, single-engine Compose operation journal.
package operations

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

const SchemaVersion = 1
const MaxMessages = 200
const MaxMessageBytes = 4096

var ErrFull = errors.New("Compose operation queue is full")
var ErrConflict = errors.New("idempotency key was already used for a different action")

type Operation struct {
	ExpectedImages map[string]string `json:"expectedImages,omitempty"`
	ID             string            `json:"id"`
	Project        string            `json:"project"`
	Action         string            `json:"action"`
	Target         string            `json:"target,omitempty"`
	State          string            `json:"state"`
	Phase          string            `json:"phase,omitempty"`
	SelfUpdate     bool              `json:"selfUpdate,omitempty"`
	NeedsAttention bool              `json:"needsAttention,omitempty"`
	Worker         string            `json:"worker,omitempty"`
	QueuedAt       string            `json:"queuedAt"`
	StartedAt      string            `json:"startedAt,omitempty"`
	FinishedAt     string            `json:"finishedAt,omitempty"`
	Messages       []string          `json:"messages"`
	Error          string            `json:"error,omitempty"`
	QueuePosition  int               `json:"queuePosition"`
}

type Store struct {
	db           *sql.DB
	coordination *sql.DB
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	fail := func(err error) (*Store, error) { db.Close(); return nil, err }
	if _, err = db.Exec(`PRAGMA busy_timeout=10000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;`); err != nil {
		return fail(err)
	}
	var version int
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fail(err)
	}
	if version != 0 && version != SchemaVersion {
		return fail(fmt.Errorf("unsupported operations schema %d", version))
	}
	if version == 0 {
		_, err = db.Exec(`BEGIN IMMEDIATE; CREATE TABLE IF NOT EXISTS operations (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, project TEXT NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, request_key TEXT UNIQUE, state TEXT NOT NULL, attention INTEGER NOT NULL DEFAULT 0, executed INTEGER NOT NULL DEFAULT 0, body TEXT NOT NULL); PRAGMA user_version=1; COMMIT;`)
		if err != nil {
			return fail(err)
		}
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS operation_requests (request_key TEXT PRIMARY KEY, operation_id TEXT NOT NULL); INSERT OR IGNORE INTO operation_requests SELECT request_key,id FROM operations WHERE request_key IS NOT NULL;`); err != nil {
		return fail(err)
	}
	// Reserve a different SQLite file: holding a writer reservation on the
	// operation journal itself would deadlock Claim and helper result writes.
	coordination, err := sql.Open("sqlite", path+".coordinator-lock")
	if err != nil {
		return fail(err)
	}
	coordination.SetMaxOpenConns(1)
	coordination.SetMaxIdleConns(0)
	return &Store{db: db, coordination: coordination}, nil
}
func (s *Store) Close() error {
	return errors.Join(s.coordination.Close(), s.db.Close())
}
func bound(v string) string {
	v = strings.ToValidUTF8(v, "")
	if len(v) > MaxMessageBytes {
		n := MaxMessageBytes
		for !utf8.RuneStart(v[n]) {
			n--
		}
		v = strings.Clone(v[:n])
	}
	return v
}
func (s *Store) transaction(fn func(*sql.Tx) error) error {
	// A no-op write obtains SQLite's cross-process writer reservation before reads.
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE operations SET seq=seq WHERE seq=-1`); err != nil {
		return err
	}
	var version int
	if err = tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version != SchemaVersion {
		return fmt.Errorf("unsupported operations schema %d", version)
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func decode(body string) (Operation, error) {
	var o Operation
	err := json.Unmarshal([]byte(body), &o)
	return o, err
}
func save(tx *sql.Tx, o Operation) error {
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE operations SET state=?,attention=?,body=? WHERE id=?`, o.State, o.NeedsAttention, string(b), o.ID)
	return err
}
func (s *Store) Submit(project, action, target, key string) (Operation, error) {
	var out Operation
	err := s.transaction(func(tx *sql.Tx) error {
		if key != "" {
			var body string
			var existingID string
			err := tx.QueryRow(`SELECT operation_id FROM operation_requests WHERE request_key=?`, key).Scan(&existingID)
			if err == nil {
				err = tx.QueryRow(`SELECT body FROM operations WHERE id=?`, existingID).Scan(&body)
				if err == sql.ErrNoRows {
					return ErrConflict
				}
			}
			if err == nil {
				var e error
				out, e = decode(body)
				if e != nil {
					return e
				}
				if out.Project != project || out.Action != action || out.Target != target {
					return ErrConflict
				}
				return nil
			}
			if err != sql.ErrNoRows {
				return err
			}
		}
		var body string
		err := tx.QueryRow(`SELECT body FROM operations WHERE project=? AND action=? AND target=? AND state IN ('queued','running') ORDER BY seq LIMIT 1`, project, action, target).Scan(&body)
		if err == nil {
			out, err = decode(body)
			if err == nil && key != "" {
				_, err = tx.Exec(`INSERT INTO operation_requests(request_key,operation_id) VALUES(?,?)`, key, out.ID)
			}
			return err
		}
		if err != sql.ErrNoRows {
			return err
		}
		var count int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM operations WHERE state='queued'`).Scan(&count); err != nil {
			return err
		}
		if count >= 32 {
			return ErrFull
		}
		bytes := make([]byte, 16)
		if _, err = rand.Read(bytes); err != nil {
			return err
		}
		out = Operation{ID: "compose-" + hex.EncodeToString(bytes), Project: project, Action: action, Target: target, State: "queued", Phase: "preparing", QueuedAt: now(), Messages: []string{}}
		out.Worker = "services-worker-" + strings.TrimPrefix(out.ID, "compose-")
		b, _ := json.Marshal(out)
		var k any
		if key != "" {
			k = key
		}
		_, err = tx.Exec(`INSERT INTO operations(id,project,action,target,request_key,state,body) VALUES(?,?,?,?,?,'queued',?)`, out.ID, project, action, target, k, string(b))
		if err == nil && key != "" {
			_, err = tx.Exec(`INSERT INTO operation_requests(request_key,operation_id) VALUES(?,?)`, key, out.ID)
		}
		return err
	})
	if err != nil {
		return out, err
	}
	return s.Get(out.ID)
}
func (s *Store) List() ([]Operation, error) {
	rows, err := s.db.Query(`SELECT body FROM operations ORDER BY seq DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Operation{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			return nil, err
		}
		o, e := decode(body)
		if e != nil {
			return nil, e
		}
		out = append(out, o)
	}
	position := 0
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].State == "queued" {
			position++
			out[i].QueuePosition = position
		}
	}
	return out, rows.Err()
}
func (s *Store) Get(id string) (Operation, error) {
	var body string
	var position int
	err := s.db.QueryRow(`SELECT body, CASE WHEN state='queued' THEN (SELECT COUNT(*) FROM operations AS waiting WHERE waiting.state='queued' AND waiting.seq <= current.seq) ELSE 0 END FROM operations AS current WHERE id=?`, id).Scan(&body, &position)
	if err != nil {
		return Operation{}, err
	}
	o, err := decode(body)
	o.QueuePosition = position
	return o, err
}
func (s *Store) Claim() (*Operation, error) {
	var out *Operation
	err := s.transaction(func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM operations WHERE state='running' OR attention=1`).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		var body string
		err := tx.QueryRow(`SELECT body FROM operations WHERE state='queued' ORDER BY seq LIMIT 1`).Scan(&body)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		o, err := decode(body)
		if err != nil {
			return err
		}
		o.State = "running"
		o.StartedAt = now()
		o.Phase = "preparing"
		out = &o
		return save(tx, o)
	})
	return out, err
}
func (s *Store) Change(id string, fn func(*Operation)) error {
	return s.transaction(func(tx *sql.Tx) error {
		var body string
		if err := tx.QueryRow(`SELECT body FROM operations WHERE id=?`, id).Scan(&body); err != nil {
			return err
		}
		o, err := decode(body)
		if err != nil {
			return err
		}
		fn(&o)
		return save(tx, o)
	})
}
func (s *Store) BeginExecution(id string) (bool, error) {
	ok := false
	err := s.transaction(func(tx *sql.Tx) error {
		r, e := tx.Exec(`UPDATE operations SET executed=1 WHERE id=? AND state='running' AND executed=0`, id)
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		ok = n == 1
		return e
	})
	return ok, err
}
func (s *Store) Message(id, line string) error {
	return s.Change(id, func(o *Operation) {
		if o.State != "running" {
			return
		}
		o.Messages = append(o.Messages, bound(line))
		if len(o.Messages) > MaxMessages {
			o.Messages = o.Messages[len(o.Messages)-MaxMessages:]
		}
	})
}
func (s *Store) Finish(id string, runErr error, attention bool) error {
	return s.transaction(func(tx *sql.Tx) error {
		var body string
		if err := tx.QueryRow(`SELECT body FROM operations WHERE id=?`, id).Scan(&body); err != nil {
			return err
		}
		o, err := decode(body)
		if err != nil {
			return err
		}
		if o.State != "running" {
			return nil
		}
		o.State = "complete"
		o.Phase = "complete"
		if runErr != nil {
			o.State = "error"
			o.Error = bound(runErr.Error())
			o.Phase = "error"
		}
		o.NeedsAttention = attention
		if attention {
			o.Phase = "interrupted"
		}
		o.FinishedAt = now()
		if err = save(tx, o); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM operations WHERE seq IN (SELECT seq FROM operations WHERE state IN ('complete','error') AND attention=0 ORDER BY seq DESC LIMIT -1 OFFSET 100)`)
		return err
	})
}
