//go:build integration

package db_test

import (
	"context"
	"testing"

	boarddb "github.com/chai-rs/handdraw-server/internal/board/infra/db"
	"github.com/chai-rs/handdraw-server/internal/board/model"
	documentcodec "github.com/chai-rs/handdraw-server/internal/document/infra/ygo"
	documentmodel "github.com/chai-rs/handdraw-server/internal/document/model"
	documentservice "github.com/chai-rs/handdraw-server/internal/document/service"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/stretchr/testify/require"
)

// TestDocumentPersistenceGuards proves valid starter bytes survive SQL, while RLS and revisions guard writes.
func (suite *repositorySuite) TestDocumentPersistenceGuards() {
	t := suite.T()
	s := suite.setup(t)
	board, err := model.NewBoard(model.NewBoardParams{WorkspaceID: s.ws, CreatedBy: s.owner, Name: "Starter", Status: model.StatusActive})
	require.NoError(t, err)
	initial, err := documentservice.NewInitialBuilder(documentcodec.Codec{}).Build(board.ID(), "get_started")
	require.NoError(t, err)
	require.NoError(t, runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		_, err := boarddb.NewBoardRepository().Create(ctx, board, model.InitialDocument{State: initial.State, SchemaVersion: initial.SchemaVersion})
		return err
	}))
	var loaded []byte
	require.NoError(t, runTransaction(t.Context(), suite.requestDB, s.viewer, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		return tx.NewRaw("SELECT state FROM handdraw.board_documents WHERE board_id=?", board.ID()).Scan(ctx, &loaded)
	}))
	require.Equal(t, initial.State, loaded)
	snapshot, err := documentcodec.Codec{}.Decode(loaded, documentmodel.Validation{BoardID: board.ID()})
	require.NoError(t, err)
	require.Equal(t, []string{initial.PageID}, snapshot.PageOrder)
	update := func(actor string, revision int64) (int64, error) {
		var rows int64
		err := runTransaction(t.Context(), suite.requestDB, actor, func(ctx context.Context) error {
			tx, err := rlstx.Current(ctx)
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, "UPDATE handdraw.board_documents SET state=?,revision=revision+1,updated_by=?,updated_at=clock_timestamp() WHERE board_id=? AND revision=?", initial.State, actor, board.ID(), revision)
			if err != nil {
				return err
			}
			rows, err = result.RowsAffected()
			return err
		})
		return rows, err
	}
	rows, err := update(s.viewer, 1)
	require.NoError(t, err)
	require.Zero(t, rows)
	rows, err = update(s.editor, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, rows)
	rows, err = update(s.owner, 1)
	require.NoError(t, err)
	require.Zero(t, rows)
	for _, tc := range []struct{ name, sql string }{
		{"revision skipped", "UPDATE handdraw.board_documents SET revision=revision+2 WHERE board_id=?"},
		{"state without revision", "UPDATE handdraw.board_documents SET state=decode('0000','hex') WHERE board_id=?"},
		{"delete durable row", "DELETE FROM handdraw.board_documents WHERE board_id=?"},
		{"immutable board ID", "UPDATE handdraw.board_documents SET board_id=board_id WHERE board_id=?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
				tx, err := rlstx.Current(ctx)
				if err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, tc.sql, board.ID())
				return err
			})
			require.Error(t, err)
		})
	}
	_, err = suite.adminDB.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='ended',access_expires_at=clock_timestamp() WHERE workspace_id=?", s.ws)
	require.NoError(t, err)
	rows, err = update(s.owner, 2)
	require.NoError(t, err)
	require.Zero(t, rows)
}

// TestBoardRequiresDocumentAtCommit rejects incomplete atomic creation even when SQL is sent directly.
func (suite *repositorySuite) TestBoardRequiresDocumentAtCommit() {
	t := suite.T()
	s := suite.setup(t)
	id, err := resourceid.New(model.BoardIDPrefix)
	require.NoError(t, err)
	err = runTransaction(t.Context(), suite.requestDB, s.owner, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO handdraw.boards(id,workspace_id,name,status,created_by) VALUES (?,?,'Incomplete','active',?)", id, s.ws, s.owner)
		return err
	})
	require.Error(t, err)
	var count int
	require.NoError(t, suite.adminDB.NewRaw("SELECT count(*) FROM handdraw.boards WHERE id=?", id).Scan(t.Context(), &count))
	require.Zero(t, count)
}
