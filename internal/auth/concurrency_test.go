package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"modernc.org/sqlite"
)

const writeLockSQL = "UPDATE auth_schema SET version=version"

// Intercept the real driver's statements rather than adding production hooks or
// replacing SQLite. Each Store still has its own connection to the same file.
type observedConnector struct {
	path          string
	before, after func(string)
}

func (c observedConnector) Driver() driver.Driver { return &sqlite.Driver{} }
func (c observedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Driver().Open(c.path)
	if err != nil {
		return nil, err
	}
	if _, err = conn.(driver.ExecerContext).ExecContext(ctx, "PRAGMA busy_timeout=10000; PRAGMA foreign_keys=ON", nil); err != nil {
		conn.Close()
		return nil, err
	}
	return &observedConn{Conn: conn, before: c.before, after: c.after}, nil
}

type observedConn struct {
	driver.Conn
	before, after func(string)
}

func (c *observedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.before != nil {
		c.before(query)
	}
	result, err := c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
	if err == nil && c.after != nil {
		c.after(query)
	}
	return result, err
}

func (c *observedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func observedStores(t *testing.T, before, after func(string)) (*Store, *Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth.db")
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	return observeStore(t, path, before, after), other
}

func observeStore(t *testing.T, path string, before, after func(string)) *Store {
	t.Helper()
	db := sql.OpenDB(observedConnector{path: path, before: before, after: after})
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	t.Cleanup(func() { s.Close() })
	return s
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for controlled database operation")
	}
}

func awaitError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for authentication operation")
		return nil
	}
}

func TestAuthenticateKeyExpiresWhileWaitingForWriteLock(t *testing.T) {
	attempted := make(chan struct{}, 1)
	authenticator, blocker := observedStores(t, func(query string) {
		if query == writeLockSQL {
			attempted <- struct{}{}
		}
	}, nil)
	key, token, err := blocker.CreateKey("reader", []string{"services:read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(3 * time.Second).Truncate(time.Second)
	if _, err = blocker.db.Exec("UPDATE api_keys SET expires_at=? WHERE id=?", expires.Unix(), key.ID); err != nil {
		t.Fatal(err)
	}
	lock, err := blocker.beginWrite()
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	result := make(chan error, 1)
	go func() { _, err := authenticator.AuthenticateKey(token); result <- err }()
	awaitSignal(t, attempted)
	if !time.Now().Before(expires) {
		t.Fatal("test precondition: authentication must start before expiry")
	}
	// Only the expiry boundary uses wall time; statement ordering uses channels
	// and a real SQLite writer lock, not a scheduling sleep.
	<-time.After(time.Until(expires))
	select {
	case <-result:
		t.Fatal("authentication completed while another Store held the write lock")
	default:
	}
	if err = lock.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = awaitError(t, result); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("key expired during write-lock wait: got %v, want unauthorized", err)
	}
	var lastUsed int64
	if err = blocker.db.QueryRow("SELECT last_used_at FROM api_keys WHERE id=?", key.ID).Scan(&lastUsed); err != nil {
		t.Fatal(err)
	}
	if lastUsed != 0 {
		t.Fatal("expired authentication updated last_used_at")
	}
}

func TestPasswordChangeBetweenVerificationAndSessionInsertion(t *testing.T) {
	verified := make(chan struct{})
	resume := make(chan struct{})
	login, changer := observedStores(t, func(query string) {
		if query == writeLockSQL {
			close(verified)
			<-resume
		}
	}, nil)
	// Register after Store cleanup so a failed assertion always releases Login.
	defer close(resume)
	user, err := changer.Bootstrap("admin", password)
	if err != nil {
		t.Fatal(err)
	}
	_, oldSession, err := changer.Login("admin", password)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, token, err := login.Login("admin", password)
		if token != "" {
			result <- errors.New("obsolete credential returned a session token")
			return
		}
		result <- err
	}()
	// Login reaches beginWrite only after successfully comparing the password.
	awaitSignal(t, verified)
	const replacement = "a replacement long password"
	// Both interactive password changes and operator resets use ChangePassword.
	if err = changer.ChangePassword(user.ID, replacement); err != nil {
		t.Fatal(err)
	}
	resume <- struct{}{}
	if err = awaitError(t, result); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("login with replaced credential: got %v, want unauthorized", err)
	}
	var sessions int
	if err = changer.db.QueryRow("SELECT count(*) FROM sessions WHERE user_id=?", user.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Fatalf("password replacement left %d sessions", sessions)
	}
	if _, err = changer.Session(oldSession); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("preexisting session survived password change: %v", err)
	}
	if _, _, err = changer.Login("admin", password); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old password still accepted: %v", err)
	}
	if _, token, err := changer.Login("admin", replacement); err != nil || token == "" {
		t.Fatalf("replacement password cannot log in: %v", err)
	}
}

func TestAuthenticateKeyRevokeAcrossStores(t *testing.T) {
	t.Run("revocation_commits_before_authentication_lock", func(t *testing.T) {
		attempted := make(chan struct{})
		resume := make(chan struct{})
		authenticator, revoker := observedStores(t, func(query string) {
			if query == writeLockSQL {
				close(attempted)
				<-resume
			}
		}, nil)
		defer close(resume)
		key, token, err := revoker.CreateKey("reader", []string{"services:read"}, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { _, err := authenticator.AuthenticateKey(token); result <- err }()
		awaitSignal(t, attempted)
		if err = revoker.RevokeKey(key.ID); err != nil {
			t.Fatal(err)
		}
		resume <- struct{}{}
		if err = awaitError(t, result); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("authentication after concurrent revoke: %v", err)
		}
		assertNoKeys(t, revoker)
	})

	t.Run("authentication_lock_precedes_revocation", func(t *testing.T) {
		locked := make(chan struct{})
		resume := make(chan struct{})
		authenticator, setup := observedStores(t, nil, func(query string) {
			if query == writeLockSQL {
				close(locked)
				<-resume
			}
		})
		defer close(resume)
		// A third connection observes the revoke attempt while authentication owns
		// the SQLite write lock. No process-local mutex can satisfy this test.
		var sequence int
		var name, path string
		if err := setup.db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
			t.Fatal(err)
		}
		attempted := make(chan struct{})
		revoker := observeStore(t, path, func(query string) {
			if query == writeLockSQL {
				close(attempted)
			}
		}, nil)
		key, token, err := setup.CreateKey("reader", []string{"services:read"}, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		authResult := make(chan error, 1)
		go func() { _, err := authenticator.AuthenticateKey(token); authResult <- err }()
		awaitSignal(t, locked)
		revokeResult := make(chan error, 1)
		go func() { revokeResult <- revoker.RevokeKey(key.ID) }()
		awaitSignal(t, attempted)
		select {
		case err = <-revokeResult:
			t.Fatalf("revocation completed before authentication released writer lock: %v", err)
		default:
		}
		resume <- struct{}{}
		if err = awaitError(t, authResult); err != nil {
			t.Fatalf("authentication holding earlier writer lock: %v", err)
		}
		if err = awaitError(t, revokeResult); err != nil {
			t.Fatalf("waiting revocation failed: %v", err)
		}
		if _, err = setup.AuthenticateKey(token); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("authentication after revocation committed: %v", err)
		}
		assertNoKeys(t, setup)
	})
}

func assertNoKeys(t *testing.T, s *Store) {
	t.Helper()
	keys, err := s.ListKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("revoked key remains stored: %d keys", len(keys))
	}
}
