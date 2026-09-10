// Package db keeps collaboration on one pinned PostgreSQL session and actor-scoped transactions.
package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sync"

	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/uptrace/bun"
)

const authorityLock int64 = 724104182601

// Authority serializes beta collaboration on a dedicated, non-reconnecting database connection.
// A second runtime cannot start until PostgreSQL releases the first runtime's session lock.
type Authority struct {
	mu     sync.Mutex
	conn   bun.Conn
	closed bool
}

// Acquire requires a direct/session-pooled PostgreSQL connection; transaction pooling is unsupported.
func Acquire(ctx context.Context, pool *bun.DB) (*Authority, error) {
	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, err
	}

	var locked bool
	if err = conn.NewRaw("SELECT pg_try_advisory_lock(?)", authorityLock).Scan(ctx, &locked); err != nil || !locked {
		_ = conn.Close()
		return nil, errors.New("collaboration authority unavailable")
	}

	return &Authority{conn: conn}, nil
}

// Run never retries on another connection, including after an uncertain commit.
func (a *Authority) Run(ctx context.Context, actor string, fn func(context.Context) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return errors.New("collaboration authority closed")
	}

	return rlstx.RunOnConnection(ctx, a.conn, actor, fn)
}

// Check fails readiness if the pinned session is lost; it cannot reacquire the lock.
func (a *Authority) Check(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return errors.New("collaboration authority closed")
	}

	return a.conn.PingContext(ctx)
}

// Close releases the authority after transport has closed and joined every socket handler.
func (a *Authority) Close(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.closed {
		return nil
	}

	a.closed = true
	_, err := a.conn.ExecContext(ctx, "SELECT pg_advisory_unlock(?)", authorityLock)

	// A lost pinned connection already released its PostgreSQL session lock.
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, driver.ErrBadConn) {
		err = nil
	}

	closeErr := a.conn.Close()
	if errors.Is(closeErr, sql.ErrConnDone) {
		closeErr = nil
	}

	return errors.Join(err, closeErr)
}
