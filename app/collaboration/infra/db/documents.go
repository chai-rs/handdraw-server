package db

import (
	"context"
	"database/sql"
	"errors"

	assetdb "github.com/chai-rs/handdraw-server/app/asset_management/infra/db"
	appmodel "github.com/chai-rs/handdraw-server/app/collaboration/model"
	"github.com/chai-rs/handdraw-server/internal/collaboration/model"
	document "github.com/chai-rs/handdraw-server/internal/document/model"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
)

type documents struct{}

var _ appmodel.Documents = (*documents)(nil)

// NewDocuments has no root pool; every query requires the verified transaction context.
func NewDocuments() *documents { return &documents{} }

// LockWorkspace follows the same parent-first order as membership, billing and board deletion.
func (*documents) LockWorkspace(ctx context.Context, workspace string) error {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return err
	}

	var allowed bool
	if err = tx.NewRaw("SELECT handdraw.lock_content_workspace(?)", workspace).Scan(ctx, &allowed); err != nil {
		return err
	}

	if !allowed {
		return model.ErrSession
	}

	return nil
}

// Load reads only RLS-visible committed content with the supported schema.
func (*documents) Load(ctx context.Context, board string) (model.Document, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Document{}, err
	}

	var row struct {
		State    []byte `bun:"state"`
		Revision int64  `bun:"revision"`
	}

	err = tx.NewRaw("SELECT state, revision FROM handdraw.board_documents WHERE board_id=? AND schema_version=1", board).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Document{}, model.ErrSession
	}

	return model.Document{State: row.State, Revision: row.Revision}, err
}

// Save performs CAS under RLS; callers publish only after their enclosing transaction commits.
func (*documents) Save(ctx context.Context, board string, revision int64, state []byte) error {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, "UPDATE handdraw.board_documents SET state=?,revision=revision+1,updated_by=handdraw.current_actor(),updated_at=clock_timestamp() WHERE board_id=? AND revision=? AND schema_version=1", state, board, revision)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows != 1 {
		return model.ErrConflict
	}

	return nil
}

// Scope resolves immutable asset provenance inside the actor transaction.
func (*documents) Scope(ctx context.Context, board string) (document.Validation, error) {
	return assetdb.ContentScope(ctx, board)
}
