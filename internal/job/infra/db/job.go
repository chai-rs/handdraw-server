// Package db separates request job enqueue from the restricted cleanup-worker connection.
package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chai-rs/handdraw-server/internal/job/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/uptrace/bun"
)

type repository struct{}

var _ model.Repository = (*repository)(nil)

// New constructs actor-scoped request persistence.
func New() *repository { return &repository{} }

// Enqueue stores one cleanup job for the board already marked deleting in this transaction.
func (r *repository) Enqueue(ctx context.Context, w, b string) (model.Deletion, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Deletion{}, err
	}

	id, err := resourceid.New(model.IDPrefix)
	if err != nil {
		return model.Deletion{}, err
	}

	_, err = tx.ExecContext(ctx, "INSERT INTO handdraw.board_deletion_jobs(id,board_id,workspace_id) VALUES (?,?,?) ON CONFLICT(board_id) DO NOTHING", id, b, w)
	if err != nil {
		return model.Deletion{}, model.ErrUnavailable
	}

	return r.Get(ctx, b)
}

// Get returns the job for a board only while the actor retains workspace membership.
func (*repository) Get(ctx context.Context, b string) (model.Deletion, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Deletion{}, err
	}

	var r struct {
		ID          string `bun:"id"`
		BoardID     string `bun:"board_id"`
		WorkspaceID string `bun:"workspace_id"`
		Status      string `bun:"status"`
	}

	err = tx.NewRaw("SELECT id,board_id,workspace_id,status FROM handdraw.board_deletion_jobs WHERE board_id=?", b).Scan(ctx, &r)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Deletion{}, nil
	}

	if err != nil {
		return model.Deletion{}, model.ErrUnavailable
	}

	return model.Deletion{ID: r.ID, BoardID: r.BoardID, WorkspaceID: r.WorkspaceID, Status: r.Status}, nil
}

type worker struct{ db *bun.DB }

var _ model.Worker = (*worker)(nil)

// NewWorker binds the dedicated function-only worker pool.
func NewWorker(db *bun.DB) *worker { return &worker{db: db} }

// Check rejects credentials with direct table access or request/resolver capability.
func (w *worker) Check(ctx context.Context) error {
	var ok bool

	err := w.db.NewRaw(`SELECT NOT rolsuper AND NOT rolbypassrls AND NOT rolcreaterole AND NOT rolcreatedb
 AND pg_has_role(current_user,'handdraw_cleanup_worker','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_request','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_access_owner','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_identity_resolver','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_billing_worker','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_quota_worker','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_idempotency_gc','MEMBER')
 AND NOT has_table_privilege(current_user,'handdraw.boards','DELETE')
 AND NOT has_schema_privilege(current_user,'handdraw','CREATE')
 AND has_function_privilege(current_user,'handdraw.run_board_cleanup()','EXECUTE') FROM pg_roles WHERE rolname=current_user`).Scan(ctx, &ok)
	if err != nil || !ok {
		return model.ErrUnavailable
	}

	return nil
}

// RunOne uses one statement/transaction, so process failure leaves an unfinished job queued.
func (w *worker) RunOne(ctx context.Context) (int, error) {
	var n int

	err := w.db.NewRaw("SELECT handdraw.run_board_cleanup()").Scan(ctx, &n)
	if err != nil {
		return 0, model.ErrUnavailable
	}

	return n, nil
}
