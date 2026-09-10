// Package db implements board-domain repositories on the workflow's Bun transaction.
// Repositories have no pool to fall back to and never call another domain.
package db

import (
	"database/sql"
	"errors"
	"time"

	"github.com/chai-rs/handdraw-server/internal/board/model"
	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	errx "github.com/chai-rs/handdraw-server/pkg/error"
	"github.com/uptrace/bun"
)

type projectRow struct {
	bun.BaseModel `bun:"table:handdraw.projects,alias:p"`
	ID            string     `bun:"id,pk"`
	WorkspaceID   string     `bun:"workspace_id"`
	Name          string     `bun:"name"`
	CreatedBy     string     `bun:"created_by"`
	Revision      int64      `bun:"revision"`
	CreatedAt     time.Time  `bun:"created_at"`
	UpdatedAt     time.Time  `bun:"updated_at"`
	DeletedAt     *time.Time `bun:"deleted_at"`
}

func (r projectRow) toModel() (model.Project, error) {
	return model.RehydrateProject(model.RehydrateProjectParams{ID: r.ID, WorkspaceID: r.WorkspaceID, Name: r.Name, CreatedBy: r.CreatedBy, Revision: r.Revision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
}

type boardRow struct {
	bun.BaseModel `bun:"table:handdraw.boards,alias:b"`
	ID            string       `bun:"id,pk"`
	WorkspaceID   string       `bun:"workspace_id"`
	ProjectID     *string      `bun:"project_id"`
	Name          string       `bun:"name"`
	Status        model.Status `bun:"status"`
	CreatedBy     string       `bun:"created_by"`
	Revision      int64        `bun:"metadata_revision"`
	CreatedAt     time.Time    `bun:"created_at"`
	UpdatedAt     time.Time    `bun:"updated_at"`
	DeletedAt     *time.Time   `bun:"deleted_at"`
}

func (r boardRow) toModel() (model.Board, error) {
	project := ""
	if r.ProjectID != nil {
		project = *r.ProjectID
	}

	return model.RehydrateBoard(model.RehydrateBoardParams{ID: r.ID, WorkspaceID: r.WorkspaceID, ProjectID: project, Name: r.Name, Status: r.Status, CreatedBy: r.CreatedBy, Revision: r.Revision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt})
}

func optional(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func persistenceError(err error) error {
	if err == nil {
		return nil
	}

	if bunx.SQLState(err) == "42501" {
		return errx.Wrap(model.ErrPermissionDenied)
	}

	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrNotFound
	}

	return errx.Wrap(err)
}

func pageQuery(q *bun.SelectQuery, alias string, page model.PageRequest) *bun.SelectQuery {
	if page.After != nil {
		q = q.Where("(?.updated_at, ?.id) < (?, ?)", bun.Ident(alias), bun.Ident(alias), page.After.UpdatedAt, page.After.ID)
	}

	return q.OrderExpr("?.updated_at DESC, ?.id DESC", bun.Ident(alias), bun.Ident(alias)).Limit(page.Limit + 1)
}
