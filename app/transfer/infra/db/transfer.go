// Package db separates request enqueue from a function-only transfer worker credential.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/chai-rs/handdraw-server/app/transfer/model"
	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
)

// Repository uses the current actor transaction for requests and a separate worker pool for leases.
type Repository struct{ worker *bun.DB }

var _ model.Repository = (*Repository)(nil)

// New leaves worker operations unavailable when no worker credential is configured.
func New(worker *bun.DB) *Repository { return &Repository{worker: worker} }

func mapped(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrNotFound
	}

	var state pgdriver.Error
	if errors.As(err, &state) {
		switch state.Field('C') {
		case "HD404":
			return model.ErrNotFound
		case "HD403", "42501":
			return model.ErrDenied
		case "HD409":
			return model.ErrConflict
		case "HD400", "23514":
			return model.ErrInvalid
		case "HD413":
			return asset.ErrQuota
		}
	}

	return err
}

func exec(ctx context.Context, q string, args ...any) error {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, q, args...)

	return mapped(err)
}

// Enqueue records the actor and pins export content in the same transaction as idempotency.
func (*Repository) Enqueue(ctx context.Context, id, board, kind, format, source string, assets bool) error {
	// The source argument is the page selector for exports and an asset ID for imports.
	page := ""
	if kind == "export" {
		page = source
		source = ""
	}

	return exec(ctx, "SELECT handdraw.enqueue_transfer(?,?,?,?,?,?,?)", id, board, kind, format, source, page, assets)
}

// Get returns no lease token, source content, actor email or storage key.
func (*Repository) Get(ctx context.Context, id string) (model.Job, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Job{}, err
	}

	var job model.Job

	err = tx.NewRaw("SELECT id,board_id,kind,status,attempts,result_asset_id,error_code FROM handdraw.transfer_jobs WHERE id=?", id).Scan(ctx, &job)

	return job, mapped(err)
}

// Check requires the dedicated role and refuses request or elevated credentials.
func (r *Repository) Check(ctx context.Context) error {
	if r.worker == nil {
		return model.ErrDenied
	}

	var valid bool

	err := r.worker.NewRaw(`SELECT NOT rolsuper AND NOT rolbypassrls AND NOT rolcreaterole AND NOT rolcreatedb AND pg_has_role(current_user,'handdraw_transfer_worker','MEMBER') AND NOT pg_has_role(current_user,'handdraw_request','MEMBER') AND NOT pg_has_role(current_user,'handdraw_access_owner','MEMBER') AND NOT has_table_privilege(current_user,'handdraw.board_documents','UPDATE') AND NOT has_schema_privilege(current_user,'handdraw','CREATE') FROM pg_roles WHERE rolname=current_user`).Scan(ctx, &valid)
	if err != nil {
		return err
	}

	if !valid {
		return model.ErrDenied
	}

	return nil
}

// Lease atomically fences competing workers and recovers expired leases with a bounded retry count.
func (r *Repository) Lease(ctx context.Context, token string) (model.Task, error) {
	if r.worker == nil {
		return model.Task{}, model.ErrDenied
	}

	var raw []byte

	err := r.worker.NewRaw("SELECT handdraw.lease_transfer(?)", token).Scan(ctx, &raw)
	if err != nil {
		return model.Task{}, mapped(err)
	}

	if len(raw) == 0 || string(raw) == "null" {
		return model.Task{}, model.ErrEmpty
	}

	var task model.Task

	err = json.Unmarshal(raw, &task)

	return task, err
}

// Run reauthorizes the persisted job actor under a workspace lock before bounded storage work.
func (r *Repository) Run(ctx context.Context, task model.Task, fn func(context.Context, model.Payload) error) error {
	if r.worker == nil {
		return model.ErrDenied
	}

	return rlstx.Run(ctx, r.worker, task.Actor, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}

		var raw []byte
		if err = tx.NewRaw("SELECT handdraw.transfer_context(?,?)", task.ID, task.Token).Scan(ctx, &raw); err != nil {
			return mapped(err)
		}

		var p model.Payload
		if err = json.Unmarshal(raw, &p); err != nil {
			return err
		}

		if p.Actor != task.Actor || p.ID != task.ID {
			return model.ErrDenied
		}

		return fn(ctx, p)
	})
}

// Prepare commits reservations and deterministic object identities before any new object bytes are written.
func (*Repository) Prepare(ctx context.Context, task model.Task, assets []asset.Asset) error {
	if assets == nil {
		assets = []asset.Asset{}
	}

	raw, err := json.Marshal(assets)
	if err != nil {
		return err
	}

	return exec(ctx, "SELECT handdraw.prepare_transfer(?,?,?::jsonb)", task.ID, task.Token, string(raw))
}

// Complete publishes every import attachment and its document together, or one verified export artifact.
func (*Repository) Complete(ctx context.Context, task model.Task, state []byte) error {
	return exec(ctx, "SELECT handdraw.complete_transfer(?,?,?)", task.ID, task.Token, state)
}

// Fail records a safe error code only if this worker still owns the lease.
func (r *Repository) Fail(ctx context.Context, task model.Task, code string) error {
	_, err := r.worker.ExecContext(ctx, "SELECT handdraw.fail_transfer(?,?,?)", task.ID, task.Token, code)
	return mapped(err)
}
