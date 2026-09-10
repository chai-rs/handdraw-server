// Package rlstx carries a verified request actor and one PostgreSQL transaction through a workflow.
// Actor means authenticated user; this package does not implement an actor framework.
package rlstx

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/uptrace/bun"
)

var (
	// ErrMissingTransaction forbids repositories from falling back to an unscoped database pool.
	ErrMissingTransaction = errors.New("actor-scoped transaction required")
	// ErrNestedTransaction prevents partially committed cross-domain workflows.
	ErrNestedTransaction = errors.New("transaction already active")
)

type (
	contextKey struct{}
	scope      struct {
		tx    bun.Tx
		actor string
	}
)

// Run opens the workflow transaction after the caller has verified the user's identity.
// Errors, cancellation and panics roll back. A nested workflow must reuse the existing context.
func Run(ctx context.Context, db *bun.DB, actor string, fn func(context.Context) error) error {
	return run(ctx, db.RunInTx, actor, fn)
}

// RunOnConnection keeps actor isolation on a pinned authority connection without pool fallback.
func RunOnConnection(ctx context.Context, conn bun.Conn, actor string, fn func(context.Context) error) error {
	return run(ctx, conn.RunInTx, actor, fn)
}

func run(ctx context.Context, transact func(context.Context, *sql.TxOptions, func(context.Context, bun.Tx) error) error, actor string, fn func(context.Context) error) error {
	if err := resourceid.Validate(actor, "usr"); err != nil {
		return err
	}

	if ctx.Value(contextKey{}) != nil {
		return ErrNestedTransaction
	}

	return transact(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SELECT set_config('handdraw.user_id', ?, true)", actor); err != nil {
			return err
		}

		return fn(context.WithValue(ctx, contextKey{}, scope{tx: tx, actor: actor}))
	})
}

// Current returns the transaction associated with a verified user, or fails closed.
func Current(ctx context.Context) (bun.Tx, error) {
	value, ok := ctx.Value(contextKey{}).(scope)
	if !ok {
		return bun.Tx{}, ErrMissingTransaction
	}

	return value.tx, nil
}

// Actor returns the verified profile ID carried by the workflow transaction.
func Actor(ctx context.Context) (string, error) {
	value, ok := ctx.Value(contextKey{}).(scope)
	if !ok {
		return "", ErrMissingTransaction
	}

	return value.actor, nil
}
