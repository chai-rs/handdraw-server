package db

import (
	"context"
	"errors"

	"github.com/chai-rs/handdraw-server/internal/board/model"
	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/uptrace/bun"
)

type boardRepository struct{}

var _ model.BoardRepository = (*boardRepository)(nil)

// NewBoardRepository returns persistence without a root database handle.
func NewBoardRepository() *boardRepository { return &boardRepository{} }

func checkProject(ctx context.Context, tx bun.Tx, workspace, project string) error {
	if project == "" {
		return nil
	}

	_, err := lockProject(ctx, tx, workspace, project)
	if errors.Is(err, model.ErrNotFound) {
		return model.ErrInvalidProject
	}

	return persistenceError(err)
}

// Create inserts metadata and the application-validated document in the same transaction.
func (r *boardRepository) Create(ctx context.Context, value model.Board, document model.InitialDocument) (model.Board, error) {
	if err := document.Validate(); err != nil {
		return model.Board{}, err
	}

	tx, err := bunx.Txer(ctx)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	if err := checkProject(ctx, tx, value.WorkspaceID(), value.ProjectID()); err != nil {
		return model.Board{}, persistenceError(err)
	}

	row := boardRow{ID: value.ID(), WorkspaceID: value.WorkspaceID(), ProjectID: optional(value.ProjectID()), Name: value.Name(), Status: value.Status(), CreatedBy: value.CreatedBy(), Revision: 1}

	_, err = tx.NewInsert().Model(&row).ExcludeColumn("created_at", "updated_at").Returning("*").Exec(ctx)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	actor, err := rlstx.Actor(ctx)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO handdraw.board_documents (board_id, workspace_id, state, revision, schema_version, updated_by) VALUES (?, ?, ?, 1, ?, ?)`, row.ID, row.WorkspaceID, document.State, document.SchemaVersion, actor)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	return row.toModel()
}

// Get includes lifecycle metadata so application workflows can gate room access separately.
func (r *boardRepository) Get(ctx context.Context, workspace, id string) (model.Board, error) {
	tx, err := bunx.Txer(ctx)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	var row boardRow

	err = tx.NewSelect().Model(&row).Where("b.workspace_id = ? AND b.id = ? AND b.deleted_at IS NULL", workspace, id).Scan(ctx)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	return row.toModel()
}

// List filters grouping without confusing all projects with ungrouped boards.
func (r *boardRepository) List(ctx context.Context, workspace string, project *string, page model.PageRequest) (model.Page[model.Board], error) {
	page, err := page.Normalize(model.BoardIDPrefix)
	if err != nil {
		return model.Page[model.Board]{}, persistenceError(err)
	}

	tx, err := bunx.Txer(ctx)
	if err != nil {
		return model.Page[model.Board]{}, persistenceError(err)
	}

	rows := []boardRow{}
	q := tx.NewSelect().Model(&rows).Where("b.workspace_id = ? AND b.deleted_at IS NULL", workspace)

	if project != nil {
		if *project == "" {
			q = q.Where("b.project_id IS NULL")
		} else {
			q = q.Where("b.project_id = ?", *project)
		}
	}

	err = pageQuery(q, "b", page).Scan(ctx)
	if err != nil {
		return model.Page[model.Board]{}, persistenceError(err)
	}

	result := model.Page[model.Board]{Items: make([]model.Board, 0, min(len(rows), page.Limit))}
	if len(rows) > page.Limit {
		rows = rows[:page.Limit]
		last := rows[len(rows)-1]
		result.Next = &model.Position{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}

	for _, row := range rows {
		value, err := row.toModel()
		if err != nil {
			return model.Page[model.Board]{}, persistenceError(err)
		}

		result.Items = append(result.Items, value)
	}

	return result, nil
}

func lockBoard(ctx context.Context, tx bun.Tx, workspace, id string, expected int64) (boardRow, error) {
	var row boardRow

	err := tx.NewSelect().Model(&row).Where("b.workspace_id = ? AND b.id = ? AND b.deleted_at IS NULL", workspace, id).For("UPDATE").Scan(ctx)
	if err != nil {
		return boardRow{}, persistenceError(err)
	}

	if row.Revision != expected {
		return boardRow{}, model.ErrRevisionConflict
	}

	if row.Status != model.StatusActive {
		return boardRow{}, model.ErrInvalidState
	}

	return row, nil
}

// Update changes only supplied fields and never moves ownership to another workspace.
func (r *boardRepository) Update(ctx context.Context, workspace, id string, patch model.BoardPatch, expected int64) (model.Board, error) {
	tx, err := bunx.Txer(ctx)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}
	// Lock target project before board, matching creation and empty-project deletion ordering.
	if patch.Project.Set {
		if err := checkProject(ctx, tx, workspace, patch.Project.ID); err != nil {
			return model.Board{}, persistenceError(err)
		}
	}

	row, err := lockBoard(ctx, tx, workspace, id, expected)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	q := tx.NewUpdate().Model(&row).Set("metadata_revision = metadata_revision + 1").Set("updated_at = clock_timestamp()")
	if patch.Name != nil {
		q = q.Set("name = ?", *patch.Name)
	}

	if patch.Project.Set {
		q = q.Set("project_id = ?", optional(patch.Project.ID))
	}

	result, err := q.Where("b.workspace_id = ? AND b.id = ? AND b.metadata_revision = ? AND b.status = ? AND b.deleted_at IS NULL", workspace, id, expected, model.StatusActive).Returning("*").Exec(ctx)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	if count == 0 {
		return model.Board{}, model.ErrNotFound
	}

	return row.toModel()
}

// MarkDeleting closes the metadata lifecycle without deleting content or enqueueing jobs itself.
func (r *boardRepository) MarkDeleting(ctx context.Context, workspace, id string, expected int64) (model.Board, error) {
	tx, err := bunx.Txer(ctx)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	row, err := lockBoard(ctx, tx, workspace, id, expected)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	result, err := tx.NewUpdate().Model(&row).Set("status = ?", model.StatusDeleting).Set("metadata_revision = metadata_revision + 1").Set("updated_at = clock_timestamp()").Where("b.workspace_id = ? AND b.id = ? AND b.metadata_revision = ? AND b.deleted_at IS NULL", workspace, id, expected).Returning("*").Exec(ctx)
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return model.Board{}, persistenceError(err)
	}

	if count == 0 {
		return model.Board{}, model.ErrNotFound
	}

	return row.toModel()
}
