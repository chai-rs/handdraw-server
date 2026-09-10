// Package db persists workspace metadata through the transaction-local request actor and target RLS.
package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/cursor"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
)

type repository struct{}

var _ model.Repository = (*repository)(nil)

// New returns a repository with no unscoped pool fallback.
func New() *repository { return &repository{} }

type workspaceRow struct {
	bun.BaseModel  `bun:"table:handdraw.workspaces,alias:w"`
	ID             string    `bun:"id"`
	OwnerID        string    `bun:"owner_user_id"`
	Kind           string    `bun:"kind"`
	Name           string    `bun:"name"`
	Lifecycle      string    `bun:"lifecycle"`
	Revision       int64     `bun:"revision"`
	AccessRevision int64     `bun:"access_revision"`
	CreatedAt      time.Time `bun:"created_at"`
	UpdatedAt      time.Time `bun:"updated_at"`
}

func (r workspaceRow) value() (model.Workspace, error) {
	w := model.Workspace{ID: r.ID, OwnerID: r.OwnerID, Kind: r.Kind, Name: r.Name, Lifecycle: r.Lifecycle, Revision: r.Revision, AccessRevision: r.AccessRevision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	return w, w.Validate()
}

// Get hides deleted or inaccessible metadata using current-actor RLS.
func (*repository) Get(ctx context.Context, id string) (model.Workspace, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Workspace{}, err
	}

	var row workspaceRow

	err = tx.NewSelect().Model(&row).Where("w.id=? AND w.deleted_at IS NULL", id).Scan(ctx)
	if err != nil {
		return model.Workspace{}, mapError(err)
	}

	return row.value()
}

// List uses deterministic updated_at/id keyset ordering and one lookahead row.
func (*repository) List(ctx context.Context, p model.PageRequest) (model.Page, error) {
	if err := p.Validate(); err != nil {
		return model.Page{}, err
	}

	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Page{}, err
	}

	rows := []workspaceRow{}

	q := tx.NewSelect().Model(&rows).Where("w.deleted_at IS NULL").OrderExpr("w.updated_at DESC,w.id DESC").Limit(p.Limit + 1)
	if p.After != nil {
		q = q.Where("(w.updated_at,w.id)<(?,?)", p.After.UpdatedAt, p.After.ID)
	}

	if err = q.Scan(ctx); err != nil {
		return model.Page{}, mapError(err)
	}

	result := model.Page{Items: []model.Workspace{}}

	if len(rows) > p.Limit {
		rows = rows[:p.Limit]
		last := rows[len(rows)-1]
		result.Next = &cursor.Position{ID: last.ID, UpdatedAt: last.UpdatedAt}
	}

	for _, r := range rows {
		w, err := r.value()
		if err != nil {
			return model.Page{}, model.ErrUnavailable
		}

		result.Items = append(result.Items, w)
	}

	return result, nil
}

// Member validates the role against the workspace's Owner and personal/team invariants.
func (r *repository) Member(ctx context.Context, workspace, user string) (model.Member, error) {
	w, err := r.Get(ctx, workspace)
	if err != nil {
		return model.Member{}, err
	}

	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Member{}, err
	}

	var row struct {
		Role     model.Role `bun:"role"`
		Revision int64      `bun:"revision"`
	}

	err = tx.NewRaw("SELECT role,revision FROM handdraw.workspace_members WHERE workspace_id=? AND user_id=?", workspace, user).Scan(ctx, &row)
	if err != nil {
		return model.Member{}, mapError(err)
	}

	m := model.Member{WorkspaceID: workspace, UserID: user, Role: row.Role, Revision: row.Revision}

	return m, m.ValidateFor(w)
}

// Rename locks the parent row before rechecking Owner/entitlement and the expected revision.
func (r *repository) Rename(ctx context.Context, id string, name model.Name, expected int64) (model.Workspace, error) {
	if name.Validate() != nil || expected < 1 {
		return model.Workspace{}, model.ErrInvalid
	}

	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Workspace{}, err
	}

	var row workspaceRow

	err = tx.NewSelect().Model(&row).Where("w.id=? AND w.deleted_at IS NULL", id).For("UPDATE").Scan(ctx)
	if err != nil {
		return model.Workspace{}, mapError(err)
	}

	actor, err := rlstx.Actor(ctx)
	if err != nil {
		return model.Workspace{}, err
	}

	var writable bool
	if err = tx.NewRaw("SELECT handdraw.can_write_workspace(?)", id).Scan(ctx, &writable); err != nil {
		return model.Workspace{}, mapError(err)
	}

	if row.OwnerID != actor || !writable {
		return model.Workspace{}, model.ErrForbidden
	}

	if row.Revision != expected {
		return model.Workspace{}, model.ErrRevisionConflict
	}

	_, err = tx.NewUpdate().Model(&row).Set("name=?", string(name)).Set("revision=revision+1").Set("updated_at=clock_timestamp()").Where("w.id=? AND w.revision=?", id, expected).Returning("id,owner_user_id,kind,name,lifecycle,revision,access_revision,created_at,updated_at").Exec(ctx)
	if err != nil {
		return model.Workspace{}, mapError(err)
	}

	return row.value()
}

func mapError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrNotFound
	}

	var pg pgdriver.Error
	if errors.As(err, &pg) && pg.Field('C') == "42501" {
		return model.ErrForbidden
	}

	return model.ErrUnavailable
}
