package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/driver"
	"github.com/xraph/grove/drivers/pgdriver"
)

// DefaultLeaderLock is the advisory lock name the extension's leader
// election uses when you do not pick one.
const DefaultLeaderLock = "ctrlplane-scheduler"

// LeaderElector elects one leader among replicas that share a Postgres
// database, using a session-level advisory lock. It satisfies
// worker.LeaderElector.
//
// The leader keeps one pooled connection checked out for as long as it
// leads, because the lock lives exactly as long as that session. If the
// connection dies, Postgres drops the lock and another replica takes it on
// its next check.
type LeaderElector struct {
	pg   *pgdriver.PgDB
	lock string

	mu   sync.Mutex
	conn driver.DedicatedConn
}

// NewLeaderElector returns an elector over the advisory lock named lock.
// Replicas that should elect one leader between them must use the same
// name.
func NewLeaderElector(db *grove.DB, lock string) *LeaderElector {
	return &LeaderElector{pg: pgdriver.Unwrap(db), lock: lock}
}

// LeaderElector returns an elector over this store's database.
func (s *Store) LeaderElector(lock string) *LeaderElector {
	return &LeaderElector{pg: s.pg, lock: lock}
}

// IsLeader reports whether this replica holds the lock, trying to take it
// when it does not. While the lock is held it costs one round trip on the
// held connection.
func (e *LeaderElector) IsLeader(ctx context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.conn != nil {
		_, err := e.conn.Exec(ctx, "SELECT 1")
		if err == nil {
			return true, nil
		}

		// A cancelled or expired ctx says nothing about the session.
		// Keep the connection and the lock, and report the error.
		if ctx.Err() != nil {
			return false, fmt.Errorf("ctrlplane/postgres: leader election: %w", ctx.Err())
		}

		// The session is most likely gone, and its lock with it.
		e.drop(e.conn)
		e.conn = nil
	}

	conn, err := e.pg.AcquireConn(ctx)
	if err != nil {
		return false, fmt.Errorf("ctrlplane/postgres: leader election: %w", err)
	}

	var acquired bool

	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", e.lock).Scan(&acquired); err != nil {
		e.drop(conn)

		return false, fmt.Errorf("ctrlplane/postgres: leader election: try lock %q: %w", e.lock, err)
	}

	if !acquired {
		conn.Release()

		return false, nil
	}

	e.conn = conn

	return true, nil
}

// Resign releases the lock and returns the connection to the pool. It is a
// no-op when this replica is not the leader.
func (e *LeaderElector) Resign(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.conn == nil {
		return nil
	}

	conn := e.conn
	e.conn = nil

	var released bool

	if err := conn.QueryRow(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", e.lock).Scan(&released); err != nil {
		e.drop(conn)

		return fmt.Errorf("ctrlplane/postgres: leader election: unlock %q: %w", e.lock, err)
	}

	conn.Release()

	if !released {
		return errors.New("ctrlplane/postgres: leader election: lock " + e.lock + " was not held")
	}

	return nil
}

// drop returns a connection to the pool after making sure it holds no
// advisory lock, so a lock can never sit in an idle pooled session where
// no replica could take it. A connection that is already broken fails the
// unlock and is discarded by the pool on release, taking any lock with it.
func (e *LeaderElector) drop(conn driver.DedicatedConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Best effort: a failure here means the session is broken, which
	// releases its locks anyway.
	_, _ = conn.Exec(ctx, "SELECT pg_advisory_unlock_all()")

	conn.Release()
}
