// Package model owns workspace metadata and membership invariants independently of billing and identity.
package model

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/chai-rs/handdraw-server/pkg/cursor"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

const (
	// WorkspaceIDPrefix identifies a workspace resource.
	WorkspaceIDPrefix = "ws"
	// UserIDPrefix identifies an external profile reference.
	UserIDPrefix = "usr"
)

// Role is the persisted workspace membership permission.
type Role string

const (
	// Owner is the sole member responsible for workspace administration.
	Owner Role = "owner"
	// Editor may change content when the workspace entitlement permits it.
	Editor Role = "editor"
	// Viewer may read and comment but never edit content.
	Viewer Role = "viewer"
)

var (
	// ErrInvalid rejects corrupt metadata or invalid mutation input.
	ErrInvalid = errors.New("invalid workspace")
	// ErrNotFound hides missing and inaccessible workspaces equally.
	ErrNotFound = errors.New("workspace not found")
	// ErrRevisionConflict rejects a stale metadata mutation.
	ErrRevisionConflict = errors.New("workspace revision conflict")
	// ErrForbidden rejects writes outside the current membership permissions.
	ErrForbidden = errors.New("workspace permission denied")
	// ErrUnavailable hides persistence details at the application boundary.
	ErrUnavailable = errors.New("workspace unavailable")
)

// Workspace is the validated persisted metadata; writes are accepted only through guarded repositories.
type Workspace struct {
	ID             string    `json:"id"`
	OwnerID        string    `json:"owner_user_id"`
	Kind           string    `json:"kind"`
	Name           string    `json:"name"`
	Lifecycle      string    `json:"lifecycle"`
	Revision       int64     `json:"revision,string"`
	AccessRevision int64     `json:"access_revision,string"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Validate refuses corrupt persisted IDs, lifecycle, names and revisions without normalizing them.
func (w Workspace) Validate() error {
	if resourceid.Validate(w.ID, WorkspaceIDPrefix) != nil || resourceid.Validate(w.OwnerID, UserIDPrefix) != nil {
		return ErrInvalid
	}

	if valx.Struct(&w, valx.Field(&w.Kind, valx.In("personal", "team")), valx.Field(&w.Lifecycle, valx.In("ready", "purging")), valx.Field(&w.Revision, valx.Min(int64(1))), valx.Field(&w.AccessRevision, valx.Min(int64(1))), valx.Field(&w.CreatedAt, valx.Required), valx.Field(&w.UpdatedAt, valx.Required, valx.TimeGTE(w.CreatedAt))) != nil || Name(w.Name).Validate() != nil {
		return ErrInvalid
	}

	return nil
}

// Name is normalized at the mutation boundary and persisted as trimmed UTF-8.
type Name string

// Normalize removes surrounding user-entered whitespace.
func (n Name) Normalize() Name { return Name(strings.TrimSpace(string(n))) }

// Validate checks the persisted name contract.
func (n Name) Validate() error {
	if n != n.Normalize() || valx.Var(string(n), valx.Required, valx.RuneLength(1, 200), valx.UTF8Text) != nil {
		return ErrInvalid
	}

	return nil
}

// Member records a stable user's role in one workspace; it does not confer billing entitlement.
type Member struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Role        Role   `json:"role"`
	Revision    int64  `json:"revision,string"`
}

// ValidateFor preserves the matching Owner and personal-workspace Owner-only invariant.
func (m Member) ValidateFor(w Workspace) error {
	if w.Validate() != nil || m.WorkspaceID != w.ID || resourceid.Validate(m.UserID, UserIDPrefix) != nil || m.Revision < 1 || valx.Var(m.Role, valx.In(Owner, Editor, Viewer)) != nil {
		return ErrInvalid
	}

	if (m.Role == Owner) != (m.UserID == w.OwnerID) || (w.Kind == "personal" && m.Role != Owner) {
		return ErrInvalid
	}

	return nil
}

// CanEditContent describes the role gate; applications must also require editable entitlement.
func (r Role) CanEditContent() bool { return r == Owner || r == Editor }

// PageRequest carries an already authenticated cursor position within an actor's workspace list.
type PageRequest struct {
	Limit int              `json:"limit"`
	After *cursor.Position `json:"after,omitempty"`
}

// Validate bounds the repository query and checks cursor resource type.
func (p PageRequest) Validate() error {
	if p.Limit < 1 || p.Limit > 100 {
		return ErrInvalid
	}

	if p.After != nil {
		return p.After.Validate(WorkspaceIDPrefix)
	}

	return nil
}

// Page contains visible workspace metadata and a stable continuation position.
type Page struct {
	Items []Workspace      `json:"items"`
	Next  *cursor.Position `json:"next,omitempty"`
}

// Repository operates only in the caller's actor-scoped transaction.
//
//mockery:generate: true
type Repository interface {
	Get(context.Context, string) (Workspace, error)
	List(context.Context, PageRequest) (Page, error)
	Member(context.Context, string, string) (Member, error)
	Rename(context.Context, string, Name, int64) (Workspace, error)
}
