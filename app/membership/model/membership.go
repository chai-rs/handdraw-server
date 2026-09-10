// Package model defines the membership workflow's identity, capacity and persistence boundaries.
package model

import (
	"context"
	"time"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/cursor"
)

// Member is a public roster row without Auth email or provider identifiers.
type Member struct {
	UserID      string         `json:"user_id" bun:"user_id"`
	DisplayName string         `json:"display_name" bun:"display_name"`
	Role        workspace.Role `json:"role" bun:"role"`
	Revision    int64          `json:"revision,string" bun:"revision"`
	UpdatedAt   time.Time      `json:"-" bun:"updated_at"`
}

// AccessResult identifies an accepted grant without conflating its source with Team membership.
type AccessResult struct {
	WorkspaceID string         `json:"workspace_id"`
	BoardID     *string        `json:"board_id"`
	UserID      string         `json:"user_id"`
	Role        workspace.Role `json:"role"`
	Source      string         `json:"source"`
}

// Guest records explicit board access, which is independent of any workspace membership.
type Guest struct {
	UserID      string         `json:"user_id" bun:"user_id"`
	DisplayName string         `json:"display_name" bun:"display_name"`
	Role        workspace.Role `json:"role" bun:"role"`
	CreatedAt   time.Time      `json:"created_at" bun:"created_at"`
	ExpiresAt   *time.Time     `json:"expires_at" bun:"expires_at"`
}

// RemainingAccess describes permission left after removing a board grant.
type RemainingAccess struct {
	Role   workspace.Role `json:"role"`
	Source string         `json:"source"`
}

// Removal reports the effective remaining access rather than assuming every revoke removes membership.
type Removal struct {
	Removed         bool             `json:"removed"`
	EffectiveAccess *RemainingAccess `json:"effective_access"`
}

// PageRequest is a bounded actor-scoped keyset page.
type PageRequest struct {
	Limit int              `json:"limit"`
	After *cursor.Position `json:"after,omitempty"`
}

// Validate checks the selected resource's pagination key.
func (p PageRequest) Validate(prefix string) error {
	if p.Limit < 1 || p.Limit > 100 {
		return workspace.ErrInvalid
	}

	if p.After != nil {
		return p.After.Validate(prefix)
	}

	return nil
}

// Access evaluates current resource permissions inside the request transaction.
//
//mockery:generate: true
type Access interface {
	Require(context.Context, access.Target, access.Action) (access.Decision, error)
}

// Tokens derives an opaque invitation token from server-held key material, never resource IDs alone.
//
//mockery:generate: true
type Tokens interface {
	Issue(string) (workspace.InvitationToken, error)
}

// Repository performs guarded writes and cross-domain capacity queries under the existing transaction.
//
//mockery:generate: true
type Repository interface {
	Member(context.Context, string, string) (Member, error)
	Members(context.Context, string, PageRequest) ([]Member, *cursor.Position, error)
	ChangeMember(context.Context, workspace.RoleChange) error
	Invite(context.Context, string, string, *string, workspace.InviteParams, []byte) (string, error)
	Invitation(context.Context, string) (workspace.Invitation, error)
	Invitations(context.Context, string, PageRequest) ([]workspace.Invitation, *cursor.Position, error)
	Accept(context.Context, []byte) (AccessResult, error)
	Revoke(context.Context, string) error
	Guests(context.Context, string) ([]Guest, error)
	RemoveGuest(context.Context, string, string) (Removal, error)
	SharedBoardIDs(context.Context, PageRequest) ([]string, *cursor.Position, error)
}
