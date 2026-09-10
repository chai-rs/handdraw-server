package bunx

import (
	"context"

	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/chai-rs/handdraw-server/pkg/txer"
	"github.com/uptrace/bun"
)

// Transactioner implements the application transaction port for one verified profile.
// The caller must verify identity before constructing this request-scoped value.
type Transactioner struct {
	db    *bun.DB
	actor string
}

var _ txer.Transactioner = (*Transactioner)(nil)

// NewTransactioner binds application transactions to a database and verified user.
func NewTransactioner(db *bun.DB, actor string) *Transactioner {
	return &Transactioner{db: db, actor: actor}
}

// RunInTx uses the existing RLS-scoped transaction implementation and rejects nesting.
func (t *Transactioner) RunInTx(ctx context.Context, fn txer.TransactionFn) error {
	return rlstx.Run(ctx, t.db, t.actor, fn)
}

// Txer returns only the active request transaction; there is no root-pool fallback.
func Txer(ctx context.Context) (bun.Tx, error) { return rlstx.Current(ctx) }
