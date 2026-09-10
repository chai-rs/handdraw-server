// Package db implements transaction-scoped board application joins under target RLS.
package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/chai-rs/handdraw-server/app/board_management/model"
	board "github.com/chai-rs/handdraw-server/internal/board/model"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
)

type query struct{}

var _ model.Query = (*query)(nil)

// New constructs a query adapter without a root-pool fallback.
func New() *query { return &query{} }

// ProjectScope resolves only parents visible through current request RLS.
func (*query) ProjectScope(ctx context.Context, id string) (model.ProjectScope, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.ProjectScope{}, err
	}

	var r struct {
		WorkspaceID string `bun:"workspace_id"`
		Deleted     bool   `bun:"deleted"`
	}

	err = tx.NewRaw("SELECT workspace_id,deleted_at IS NOT NULL AS deleted FROM handdraw.projects WHERE id=?", id).Scan(ctx, &r)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ProjectScope{}, board.ErrNotFound
	}

	return model.ProjectScope{WorkspaceID: r.WorkspaceID, Deleted: r.Deleted}, err
}

// SchemaVersion reads version metadata without loading the document bytes.
func (*query) SchemaVersion(ctx context.Context, id string) (int, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return 0, err
	}

	var version int

	err = tx.NewRaw("SELECT schema_version FROM handdraw.board_documents WHERE board_id=?", id).Scan(ctx, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, board.ErrNotFound
	}

	return version, err
}

// Document loads the committed state after the workflow verifies read permission.
func (*query) Document(ctx context.Context, id string) ([]byte, int, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return nil, 0, err
	}

	var r struct {
		State   []byte `bun:"state"`
		Version int    `bun:"schema_version"`
	}

	err = tx.NewRaw("SELECT state,schema_version FROM handdraw.board_documents WHERE board_id=?", id).Scan(ctx, &r)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, board.ErrNotFound
	}

	return r.State, r.Version, err
}

// LockWorkspace serializes mutations against membership and billing before domain row locks.
func (*query) LockWorkspace(ctx context.Context, w string) error {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return err
	}

	var allowed bool
	if err = tx.NewRaw("SELECT handdraw.lock_content_workspace(?)", w).Scan(ctx, &allowed); err != nil {
		return err
	}

	if !allowed {
		return board.ErrPermissionDenied
	}

	return nil
}
