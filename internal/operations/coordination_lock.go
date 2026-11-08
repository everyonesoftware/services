package operations

import (
	"context"
	"errors"
	"time"

	"modernc.org/sqlite"
	"modernc.org/sqlite/lib"
)

// lockCoordinator serializes the entire inventory/reconcile/claim/create/start
// interval across processes sharing this journal. SQLite owns the OS lock, so a
// crashed coordinator cannot leave a persistent owner or an expiring lease.
// The lock file must never be removed while any coordinator may be using it.
func (s *Store) lockCoordinator(ctx context.Context) (func(), error) {
	conn, err := s.coordination.Conn(ctx)
	if err != nil {
		return nil, err
	}
	// Do not let SQLite's busy handler outlive the caller's deadline. Retry only
	// lock acquisition, never a Docker action or an uncertain journal operation.
	if _, err = conn.ExecContext(ctx, `PRAGMA busy_timeout=0`); err != nil {
		conn.Close()
		return nil, err
	}
	for {
		if err = ctx.Err(); err != nil {
			conn.Close()
			return nil, err
		}
		_, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`)
		if err == nil {
			// Use an explicit transaction rather than BeginTx(ctx): cancellation
			// must not release ownership before an in-flight Launch has returned.
			return func() {
				conn.ExecContext(context.Background(), `ROLLBACK`)
				// No idle connections: even a rollback failure closes the underlying
				// SQLite connection and releases its reservation.
				conn.Close()
			}, nil
		}
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || (sqliteErr.Code()&0xff != sqlite3.SQLITE_BUSY && sqliteErr.Code()&0xff != sqlite3.SQLITE_LOCKED) {
			conn.Close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			conn.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
