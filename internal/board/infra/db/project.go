package db

import (
	"context"

	"github.com/chai-rs/handdraw-server/internal/board/model"
	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/uptrace/bun"
)

type projectRepository struct{}

var _ model.ProjectRepository = (*projectRepository)(nil)

// NewProjectRepository returns project persistence that requires a rlstx.Run context.
func NewProjectRepository() *projectRepository { return &projectRepository{} }

// Create persists a new project with database-owned timestamps.
func (r *projectRepository) Create(ctx context.Context, value model.Project) (model.Project, error) {
	tx, err := bunx.Txer(ctx)
	if err != nil {
		return model.Project{}, persistenceError(err)
	}

	row := projectRow{ID: value.ID(), WorkspaceID: value.WorkspaceID(), Name: value.Name(), CreatedBy: value.CreatedBy(), Revision: 1}

	_, err = tx.NewInsert().Model(&row).ExcludeColumn("created_at", "updated_at").Returning("*").Exec(ctx)
	if err != nil {
		return model.Project{}, persistenceError(err)
	}

	return row.toModel()
}

// Get reads live metadata within the requested workspace and current actor's RLS scope.
func (r *projectRepository) Get(ctx context.Context, workspace, id string) (model.Project, error) {
	tx, err := bunx.Txer(ctx)
	if err != nil {
		return model.Project{}, persistenceError(err)
	}

	var row projectRow

	err = tx.NewSelect().Model(&row).Where("p.workspace_id = ? AND p.id = ? AND p.deleted_at IS NULL", workspace, id).Scan(ctx)
	if err != nil {
		return model.Project{}, persistenceError(err)
	}

	return row.toModel()
}

// List reads a bounded keyset page under the caller's RLS scope.
func (r *projectRepository) List(ctx context.Context, workspace string, page model.PageRequest) (model.Page[model.Project], error) {
	page, err := page.Normalize(model.ProjectIDPrefix)
	if err != nil {
		return model.Page[model.Project]{}, persistenceError(err)
	}

	tx, err := bunx.Txer(ctx)
	if err != nil {
		return model.Page[model.Project]{}, persistenceError(err)
	}

	rows := []projectRow{}

	err = pageQuery(tx.NewSelect().Model(&rows).Where("p.workspace_id = ? AND p.deleted_at IS NULL", workspace), "p", page).Scan(ctx)
	if err != nil {
		return model.Page[model.Project]{}, persistenceError(err)
	}

	result := model.Page[model.Project]{Items: make([]model.Project, 0, min(len(rows), page.Limit))}
	if len(rows) > page.Limit {
		rows = rows[:page.Limit]
		last := rows[len(rows)-1]
		result.Next = &model.Position{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}

	for _, row := range rows {
		value, err := row.toModel()
		if err != nil {
			return model.Page[model.Project]{}, persistenceError(err)
		}

		result.Items = append(result.Items, value)
	}

	return result, nil
}

func lockProject(ctx context.Context, tx bun.Tx, workspace, id string) (projectRow, error) {
	var row projectRow

	err := tx.NewSelect().Model(&row).Where("p.workspace_id = ? AND p.id = ? AND p.deleted_at IS NULL", workspace, id).For("UPDATE").Scan(ctx)

	return row, persistenceError(err)
}

// Rename compares revision while holding the same project lock used for assignment and deletion.
func (r *projectRepository) Rename(ctx context.Context, workspace, id, name string, expected int64) (model.Project, error) {
	tx, err := bunx.Txer(ctx)
	if err != nil {
		return model.Project{}, persistenceError(err)
	}

	row, err := lockProject(ctx, tx, workspace, id)
	if err != nil {
		return model.Project{}, persistenceError(err)
	}

	if row.Revision != expected {
		return model.Project{}, model.ErrRevisionConflict
	}

	result, err := tx.NewUpdate().Model(&row).Set("name = ?", name).Set("revision = revision + 1").Set("updated_at = clock_timestamp()").Where("p.workspace_id = ? AND p.id = ? AND p.revision = ? AND p.deleted_at IS NULL", workspace, id, expected).Returning("*").Exec(ctx)
	if err != nil {
		return model.Project{}, persistenceError(err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return model.Project{}, persistenceError(err)
	}

	if count == 0 {
		return model.Project{}, model.ErrNotFound
	}

	return row.toModel()
}

// DeleteEmpty soft-deletes only after checking boards while holding the parent assignment lock.
func (r *projectRepository) DeleteEmpty(ctx context.Context, workspace, id string, expected int64) error {
	tx, err := bunx.Txer(ctx)
	if err != nil {
		return persistenceError(err)
	}

	row, err := lockProject(ctx, tx, workspace, id)
	if err != nil {
		return persistenceError(err)
	}

	if row.Revision != expected {
		return model.ErrRevisionConflict
	}

	exists, err := tx.NewSelect().Model((*boardRow)(nil)).Where("b.workspace_id = ? AND b.project_id = ? AND b.deleted_at IS NULL", workspace, id).Exists(ctx)
	if err != nil {
		return persistenceError(err)
	}

	if exists {
		return model.ErrProjectNotEmpty
	}

	result, err := tx.NewUpdate().Model(&row).Set("deleted_at = clock_timestamp()").Set("updated_at = clock_timestamp()").Set("revision = revision + 1").Where("p.workspace_id = ? AND p.id = ? AND p.revision = ? AND p.deleted_at IS NULL", workspace, id, expected).Exec(ctx)
	if err != nil {
		return persistenceError(err)
	}

	count, err := result.RowsAffected()
	if err != nil {
		return persistenceError(err)
	}

	if count == 0 {
		return model.ErrNotFound
	}

	return nil
}
