// Package db owns the scoped cross-domain query used to evaluate effective access.
package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chai-rs/handdraw-server/app/access/model"
	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	billing "github.com/chai-rs/handdraw-server/internal/billing/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/uptrace/bun"
)

type repository struct{}

var _ model.Repository = (*repository)(nil)

// New binds no pool: policy reads require an existing request transaction.
func New() *repository { return &repository{} }

type row struct {
	ID              string         `bun:"id"`
	OwnerID         string         `bun:"owner_user_id"`
	Kind            string         `bun:"kind"`
	Name            string         `bun:"name"`
	Lifecycle       string         `bun:"lifecycle"`
	Revision        int64          `bun:"revision"`
	AccessRevision  int64          `bun:"access_revision"`
	CreatedAt       time.Time      `bun:"created_at"`
	UpdatedAt       time.Time      `bun:"updated_at"`
	UserID          string         `bun:"user_id"`
	Role            workspace.Role `bun:"role"`
	MemberRevision  int64          `bun:"member_revision"`
	Plan            string         `bun:"plan"`
	Mode            string         `bun:"mode"`
	GraceEndsAt     *time.Time     `bun:"grace_ends_at"`
	AccessExpiresAt *time.Time     `bun:"access_expires_at"`
	RetentionEndsAt *time.Time     `bun:"retention_ends_at"`
	QuotaBytes      int64          `bun:"quota_bytes"`
	UsedBytes       int64          `bun:"used_bytes"`
	ReservedBytes   int64          `bun:"reserved_bytes"`
	UsageRevision   int64          `bun:"usage_revision"`
	Source          string         `bun:"source"`
	BoardStatus     string         `bun:"board_status"`
}

// Load obtains membership, resource state, entitlement and accounting in one actor-scoped statement.
func (*repository) Load(ctx context.Context, target model.Target) (model.Facts, error) {
	if err := target.Validate(); err != nil {
		return model.Facts{}, err
	}

	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Facts{}, err
	}

	query := `SELECT w.id,w.owner_user_id,w.kind,w.name,w.lifecycle,w.revision,w.access_revision,w.created_at,w.updated_at,
 m.user_id,m.role,m.revision AS member_revision,e.plan,e.mode,e.grace_ends_at,e.access_expires_at,e.retention_ends_at,e.quota_bytes,
 u.used_bytes,u.reserved_bytes,u.revision AS usage_revision,`
	if target.BoardID != "" {
		query += ` b.status AS board_status `
	} else {
		query += ` '' AS board_status `
	}

	query += ` FROM handdraw.workspaces w JOIN handdraw.workspace_members m ON m.workspace_id=w.id AND m.user_id=handdraw.current_actor()
 JOIN handdraw.workspace_usage u ON u.workspace_id=w.id JOIN LATERAL handdraw.access_entitlement(w.id) e ON true `

	id := target.WorkspaceID
	if target.BoardID != "" {
		query += ` JOIN handdraw.boards b ON b.workspace_id=w.id WHERE b.id=? AND b.deleted_at IS NULL AND w.deleted_at IS NULL`
		id = target.BoardID
	} else {
		query += ` WHERE w.id=? AND w.deleted_at IS NULL`
	}

	if target.BoardID != "" {
		query = `SELECT * FROM handdraw.board_access_facts(?)`
	}

	var r row
	if err = tx.NewRaw(query, id).Scan(ctx, &r); errors.Is(err, sql.ErrNoRows) {
		return model.Facts{}, model.ErrNotFound
	} else if err != nil {
		return model.Facts{}, model.ErrUnavailable
	}

	reason := r.Mode
	if r.Mode == "editable" {
		reason = "entitled"
	} else if r.Mode == "read_only" {
		reason = "entitlement_read_only"
	}

	return model.Facts{Workspace: workspace.Workspace{ID: r.ID, OwnerID: r.OwnerID, Kind: r.Kind, Name: r.Name, Lifecycle: r.Lifecycle, Revision: r.Revision, AccessRevision: r.AccessRevision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}, Member: workspace.Member{WorkspaceID: r.ID, UserID: r.UserID, Role: r.Role, Revision: r.MemberRevision}, Entitlement: billing.Entitlement{Plan: r.Plan, Mode: r.Mode, Reason: reason, GraceEndsAt: r.GraceEndsAt, AccessExpiresAt: r.AccessExpiresAt, RetentionEndsAt: r.RetentionEndsAt, QuotaBytes: r.QuotaBytes}, Usage: asset.Usage{UsedBytes: r.UsedBytes, ReservedBytes: r.ReservedBytes, Revision: r.UsageRevision}, BoardStatus: r.BoardStatus, Source: r.Source}, nil
}

// CheckRequestPool rejects privileged, resolver or worker credentials before application routes are enabled.
func CheckRequestPool(ctx context.Context, db *bun.DB) error {
	var allowed bool

	err := db.NewRaw(`SELECT NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb AND NOT rolcreaterole
 AND pg_has_role(current_user,'handdraw_request','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_access_owner','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_identity_owner','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_billing_worker','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_quota_worker','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_idempotency_gc','MEMBER')
 AND NOT pg_has_role(current_user,'handdraw_cleanup_worker','MEMBER')
 AND NOT has_function_privilege(current_user,'handdraw.resolve_profile(text,uuid,text)','EXECUTE')
 AND NOT has_schema_privilege(current_user,'handdraw','CREATE')
 FROM pg_roles WHERE rolname=current_user`).Scan(ctx, &allowed)
	if err != nil || !allowed {
		return model.ErrUnavailable
	}

	return nil
}
