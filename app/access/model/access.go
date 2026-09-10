// Package model defines application policy facts and ports; facts come from verified, scoped persistence.
package model

import (
	"context"
	"errors"

	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	billing "github.com/chai-rs/handdraw-server/internal/billing/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// BoardIDPrefix identifies the optional board policy target.
const BoardIDPrefix = "brd"

var (
	// ErrDenied means current facts do not permit the requested action.
	ErrDenied = errors.New("permission denied")
	// ErrNotFound hides inaccessible policy targets.
	ErrNotFound = errors.New("resource not found")
	// ErrUnavailable fails closed when access facts cannot be loaded or validated.
	ErrUnavailable = errors.New("access unavailable")
)

// Target identifies either workspace metadata or a board whose workspace is resolved server-side.
type Target struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	BoardID     string `json:"board_id,omitempty"`
}

// Validate requires exactly one canonical resource selector.
func (t Target) Validate() error {
	if (t.WorkspaceID == "") == (t.BoardID == "") {
		return resourceid.ErrInvalid
	}

	if t.BoardID != "" {
		return resourceid.Validate(t.BoardID, BoardIDPrefix)
	}

	return resourceid.Validate(t.WorkspaceID, workspace.WorkspaceIDPrefix)
}

// Facts combines the minimum public projections needed for policy evaluation.
type Facts struct {
	Workspace   workspace.Workspace `json:"workspace"`
	Member      workspace.Member    `json:"member"`
	Entitlement billing.Entitlement `json:"entitlement"`
	Usage       asset.Usage         `json:"usage"`
	Source      string              `json:"source,omitempty"`
	BoardStatus string              `json:"board_status,omitempty"`
}

// Capabilities are for UI hints; each operation still evaluates current facts.
type Capabilities struct {
	CanRead         bool `json:"can_read"`
	CanEditContent  bool `json:"can_edit_content"`
	CanComment      bool `json:"can_comment"`
	CanExport       bool `json:"can_export"`
	CanManageGuests bool `json:"can_manage_guests"`
}

// Decision separates content permissions from permission to insert premium library items.
type Decision struct {
	Facts            Facts        `json:"-"`
	Capabilities     Capabilities `json:"capabilities"`
	CanInsertPremium bool         `json:"can_insert_premium"`
}

// Action names the server operation being authorized.
type Action string

const (
	// ReadMetadata permits members to recover/manage a subscription even without content entitlement.
	ReadMetadata Action = "read_metadata"
	// ReadContent requires a live or retained content entitlement.
	ReadContent Action = "read_content"
	// EditContent requires Owner/Editor membership and editable entitlement.
	EditContent Action = "edit_content"
	// RenameWorkspace is an Owner-only metadata operation.
	RenameWorkspace Action = "rename_workspace"
)

// Repository must return only current-actor-visible facts, never an unscoped or cached grant.
//
//mockery:generate: true
type Repository interface {
	Load(context.Context, Target) (Facts, error)
}

// Authenticator verifies provider identity before resolving a stable profile.
//
//mockery:generate: true
type Authenticator interface {
	Authenticate(context.Context, identity.AccessToken) (identity.Principal, error)
}

// Transactions installs a verified profile ID into one database transaction.
//
//mockery:generate: true
type Transactions interface {
	Run(context.Context, string, func(context.Context) error) error
}
