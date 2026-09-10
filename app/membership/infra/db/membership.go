// Package db keeps membership, billing-capacity and identity joins inside the request transaction.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/chai-rs/handdraw-server/app/membership/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/cursor"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/uptrace/bun/driver/pgdriver"
)

type repository struct{}

var _ model.Repository = (*repository)(nil)

// New creates a pool-free adapter which refuses unscoped operations.
func New() *repository { return &repository{} }

func mapped(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, sql.ErrNoRows) {
		return workspace.ErrNotFound
	}

	var state pgdriver.Error
	if errors.As(err, &state) {
		switch state.Field('C') {
		case "HD400":
			return workspace.ErrInvalid
		case "HD403":
			return workspace.ErrForbidden
		case "HD404":
			return workspace.ErrNotFound
		case "HD409", "23505":
			return workspace.ErrCapacity
		case "HD410":
			return workspace.ErrInvitationGone
		case "HD412":
			return workspace.ErrRevisionConflict
		}
	}

	return errors.Join(workspace.ErrUnavailable, err)
}

// Member reads the current roster projection after a guarded change.
func (*repository) Member(ctx context.Context, w, u string) (model.Member, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Member{}, err
	}

	var row model.Member

	err = tx.NewRaw(`SELECT m.user_id,p.display_name,m.role,m.revision,m.updated_at FROM handdraw.workspace_members m JOIN handdraw.profiles p ON p.id=m.user_id WHERE m.workspace_id=? AND m.user_id=?`, w, u).Scan(ctx, &row)

	return row, mapped(err)
}

// Members uses a stable user-ID order, with the timestamp retained solely for the common cursor codec.
func (*repository) Members(ctx context.Context, w string, p model.PageRequest) ([]model.Member, *cursor.Position, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return nil, nil, err
	}

	items := []model.Member{}
	query := `SELECT m.user_id,p.display_name,m.role,m.revision,m.updated_at FROM handdraw.workspace_members m JOIN handdraw.profiles p ON p.id=m.user_id WHERE m.workspace_id=?`
	args := []any{w}

	if p.After != nil {
		query += ` AND m.user_id>?`

		args = append(args, p.After.ID)
	}

	query += ` ORDER BY m.user_id LIMIT ?`

	args = append(args, p.Limit+1)
	if err = tx.NewRaw(query, args...).Scan(ctx, &items); err != nil {
		return nil, nil, mapped(err)
	}

	var next *cursor.Position

	if len(items) > p.Limit {
		items = items[:p.Limit]
		last := items[len(items)-1]
		next = &cursor.Position{ID: last.UserID, UpdatedAt: last.UpdatedAt}
	}

	return items, next, nil
}

// ChangeMember serializes the Owner check, target revision and seat allocation on the workspace row.
func (*repository) ChangeMember(ctx context.Context, p workspace.RoleChange) error {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `SELECT handdraw.change_member(?,?,?,?)`, p.WorkspaceID, p.UserID, p.Role, p.Revision)

	return mapped(err)
}

// Invite reserves an Editor seat while deduplicating a matching pending invitation.
func (*repository) Invite(ctx context.Context, id, w string, b *string, p workspace.InviteParams, digest []byte) (string, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return "", err
	}

	var result string

	err = tx.NewRaw(`SELECT handdraw.create_invitation(?,?,?,?,?,?)`, id, w, b, p.Email, p.Role, digest).Scan(ctx, &result)

	return result, mapped(err)
}

const invitationProjection = `id,workspace_id,board_id,scope,email_normalized,role,CASE WHEN status='pending' AND expires_at<=statement_timestamp() THEN 'expired' ELSE status END AS status,expires_at,created_at`

// Invitation never selects a token hash or any bearer credential.
func (*repository) Invitation(ctx context.Context, id string) (workspace.Invitation, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return workspace.Invitation{}, err
	}

	var row workspace.Invitation

	err = tx.NewRaw(`SELECT `+invitationProjection+` FROM handdraw.invitations WHERE id=?`, id).Scan(ctx, &row)

	return row, mapped(err)
}

// Invitations paginates Owner-only history by immutable creation time and ID.
func (*repository) Invitations(ctx context.Context, w string, p model.PageRequest) ([]workspace.Invitation, *cursor.Position, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return nil, nil, err
	}

	items := []workspace.Invitation{}
	query := `SELECT ` + invitationProjection + ` FROM handdraw.invitations WHERE workspace_id=?`
	args := []any{w}

	if p.After != nil {
		query += ` AND (created_at,id)<(?,?)`

		args = append(args, p.After.UpdatedAt, p.After.ID)
	}

	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`

	args = append(args, p.Limit+1)
	if err = tx.NewRaw(query, args...).Scan(ctx, &items); err != nil {
		return nil, nil, mapped(err)
	}

	var next *cursor.Position

	if len(items) > p.Limit {
		items = items[:p.Limit]
		last := items[len(items)-1]
		next = &cursor.Position{ID: last.ID, UpdatedAt: last.CreatedAt}
	}

	return items, next, nil
}

// Accept consumes a hash once after the database independently checks current confirmed recipient identity.
func (*repository) Accept(ctx context.Context, digest []byte) (model.AccessResult, error) {
	var result model.AccessResult

	err := jsonQuery(ctx, `SELECT handdraw.accept_invitation(?)`, &result, digest)

	return result, err
}

// Revoke releases pending capacity without changing already accepted membership.
func (*repository) Revoke(ctx context.Context, id string) error {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `SELECT handdraw.revoke_invitation(?)`, id)

	return mapped(err)
}

// Guests obtains a scoped Owner projection without exposing emails or workspace peers.
func (*repository) Guests(ctx context.Context, b string) ([]model.Guest, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return nil, err
	}

	items := []model.Guest{}
	err = tx.NewRaw(`SELECT * FROM handdraw.list_board_guests(?)`, b).Scan(ctx, &items)

	return items, mapped(err)
}

// RemoveGuest revokes the grant and stale pending invitations before projecting remaining membership.
func (*repository) RemoveGuest(ctx context.Context, b, u string) (model.Removal, error) {
	var result model.Removal

	err := jsonQuery(ctx, `SELECT handdraw.remove_board_guest(?,?)`, &result, b, u)

	return result, err
}

// SharedBoardIDs requires both an explicit actor grant and current board readability.
func (*repository) SharedBoardIDs(ctx context.Context, p model.PageRequest) ([]string, *cursor.Position, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return nil, nil, err
	}

	rows := []struct {
		ID        string       `bun:"id"`
		CreatedAt sql.NullTime `bun:"created_at"`
	}{}
	query := `SELECT b.id,b.created_at FROM handdraw.boards b JOIN handdraw.board_grants g ON g.board_id=b.id WHERE g.user_id=handdraw.current_actor() AND handdraw.board_content_readable(b.id)`
	args := []any{}

	if p.After != nil {
		query += ` AND b.id>?`

		args = append(args, p.After.ID)
	}

	query += ` ORDER BY b.id LIMIT ?`

	args = append(args, p.Limit+1)
	if err = tx.NewRaw(query, args...).Scan(ctx, &rows); err != nil {
		return nil, nil, mapped(err)
	}

	var next *cursor.Position

	if len(rows) > p.Limit {
		rows = rows[:p.Limit]
		last := rows[len(rows)-1]
		next = &cursor.Position{ID: last.ID, UpdatedAt: last.CreatedAt.Time}
	}

	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}

	return ids, next, nil
}

func jsonQuery(ctx context.Context, query string, result any, args ...any) error {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return err
	}

	var raw []byte
	if err = tx.NewRaw(query, args...).Scan(ctx, &raw); err != nil {
		return mapped(err)
	}

	if err = json.Unmarshal(raw, result); err != nil {
		return workspace.ErrUnavailable
	}

	return nil
}
