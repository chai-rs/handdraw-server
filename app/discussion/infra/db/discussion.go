// Package db adapts discussions to RLS reads and narrow guarded SQL functions.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/chai-rs/handdraw-server/app/discussion/model"
	comment "github.com/chai-rs/handdraw-server/internal/comment/model"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/uptrace/bun/driver/pgdriver"
)

type repository struct{}

var _ model.Repository = (*repository)(nil)

// New binds no pool; callers must enter an authenticated transaction.
func New() *repository { return &repository{} }

func mapped(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return comment.ErrNotFound
	}

	var state pgdriver.Error
	if errors.As(err, &state) {
		switch state.Field('C') {
		case "HD404":
			return comment.ErrNotFound
		case "HD403", "42501":
			return comment.ErrDenied
		case "HD412":
			return comment.ErrConflict
		case "HD400", "23514":
			return comment.ErrInvalid
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

func (*repository) Lock(ctx context.Context, b string) error {
	return exec(ctx, "SELECT handdraw.lock_discussion(?)", b)
}

func (*repository) Document(ctx context.Context, b string) ([]byte, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return nil, err
	}

	var state []byte

	err = tx.NewRaw("SELECT state FROM handdraw.board_documents WHERE board_id=?", b).Scan(ctx, &state)

	return state, mapped(err)
}

func (*repository) Thread(ctx context.Context, id string) (comment.Thread, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return comment.Thread{}, err
	}

	var r comment.Thread

	err = tx.NewSelect().TableExpr("handdraw.comment_threads").Column("id", "board_id", "anchor", "created_by", "status", "revision", "created_at", "updated_at").Where("id=?", id).Scan(ctx, &r)

	return r, mapped(err)
}

func (*repository) Comment(ctx context.Context, id string) (comment.Comment, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return comment.Comment{}, err
	}

	var r comment.Comment

	err = tx.NewSelect().TableExpr("handdraw.comments").Column("id", "board_id", "thread_id", "author_user_id", "body", "revision", "created_at", "updated_at", "deleted_at").Where("id=?", id).Scan(ctx, &r)

	return r, mapped(err)
}

func (*repository) Threads(ctx context.Context, b, status, after string, limit int) ([]comment.Thread, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return nil, err
	}

	var readable bool
	if err = tx.NewRaw("SELECT handdraw.board_content_readable(?)", b).Scan(ctx, &readable); err != nil {
		return nil, err
	}

	if !readable {
		return nil, comment.ErrNotFound
	}

	result := []comment.Thread{}

	q := tx.NewSelect().TableExpr("handdraw.comment_threads").Column("id", "board_id", "anchor", "created_by", "status", "revision", "created_at", "updated_at").Where("board_id=?", b).Where("id>?", after).OrderExpr("id ASC").Limit(limit)
	if status != "" {
		q = q.Where("status=?", status)
	}

	err = q.Scan(ctx, &result)

	return result, mapped(err)
}

func (*repository) Comments(ctx context.Context, t, after string, limit int) ([]comment.Comment, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return nil, err
	}

	result := []comment.Comment{}
	err = tx.NewSelect().TableExpr("handdraw.comments").Column("id", "board_id", "thread_id", "author_user_id", "body", "revision", "created_at", "updated_at", "deleted_at").Where("thread_id=? AND id>?", t, after).OrderExpr("id ASC").Limit(limit).Scan(ctx, &result)

	return result, mapped(err)
}

func (*repository) Create(ctx context.Context, t, c, b string, p comment.Create) error {
	anchor, err := json.Marshal(p.Anchor)
	if err != nil {
		return err
	}

	return exec(ctx, "SELECT handdraw.create_discussion(?,?,?,?::jsonb,?)", t, c, b, string(anchor), p.Body)
}

func (*repository) Reply(ctx context.Context, c, t, body string) error {
	return exec(ctx, "SELECT handdraw.reply_discussion(?,?,?)", c, t, body)
}

func (*repository) Resolve(ctx context.Context, t, status string, revision int64) error {
	return exec(ctx, "SELECT handdraw.resolve_discussion(?,?,?)", t, status, revision)
}

func (*repository) Edit(ctx context.Context, c, body string, remove bool, revision int64) error {
	return exec(ctx, "SELECT handdraw.edit_comment(?,?,?,?)", c, body, remove, revision)
}
