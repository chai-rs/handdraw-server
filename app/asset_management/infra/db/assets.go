// Package db keeps quota and lifecycle mutations in guarded actor transactions.
package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chai-rs/handdraw-server/app/asset_management/model"
	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
)

type repository struct{}

var _ model.Repository = (*repository)(nil)

// New requires the caller's verified request transaction.
func New() *repository { return &repository{} }

func mapped(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return asset.ErrNotFound
	}

	var state pgdriver.Error
	if errors.As(err, &state) {
		switch state.Field('C') {
		case "HD404":
			return asset.ErrNotFound
		case "HD403", "42501":
			return asset.ErrDenied
		case "HD409":
			return asset.ErrConflict
		case "HD413":
			return asset.ErrQuota
		case "HD400", "23514":
			return asset.ErrInvalid
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

func (*repository) Get(ctx context.Context, id string) (asset.Asset, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return asset.Asset{}, err
	}

	var a asset.Asset

	err = tx.NewSelect().TableExpr("handdraw.assets").Column("id", "workspace_id", "board_id", "uploaded_by", "purpose", "status", "size_bytes", "mime_type", "sha256", "premium", "expires_at").Where("id=?", id).Scan(ctx, &a)

	return a, mapped(err)
}

func (*repository) Lock(ctx context.Context, id string, writing bool) error {
	return exec(ctx, "SELECT handdraw.lock_asset(?,?)", id, writing)
}

func (*repository) Reserve(ctx context.Context, id, board string, p asset.Reserve) error {
	return exec(ctx, "SELECT handdraw.reserve_asset(?,?,?,?,?,?)", id, board, p.Purpose, p.Size, p.MIME, p.SHA256)
}

func (*repository) Complete(ctx context.Context, id string) error {
	return exec(ctx, "SELECT handdraw.complete_asset(?)", id)
}

// Worker owns a narrow cleanup pool and confirms object removal before releasing quota.
type Worker struct {
	db      *bun.DB
	storage asset.Storage
}

// NewWorker uses the function-only cleanup credential, independently of request transactions.
func NewWorker(db *bun.DB, storage asset.Storage) *Worker { return &Worker{db: db, storage: storage} }

// RunOne holds a workspace lock across bounded local deletion; rollback leaves the asset eligible for retry.
func (w *Worker) RunOne(ctx context.Context) (int, error) {
	count := 0
	err := w.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var row struct {
			ID string `bun:"id"`
		}

		err := tx.NewRaw("SELECT id FROM handdraw.asset_cleanup_candidate()").Scan(ctx, &row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		if err != nil {
			return err
		}

		if err = w.storage.Remove(ctx, row.ID); err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, "SELECT handdraw.finish_asset_cleanup(?)", row.ID)
		if err == nil {
			count = 1
		}

		return err
	})

	return count, err
}
