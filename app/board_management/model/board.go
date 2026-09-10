// Package model defines board application inputs and cross-domain dependencies.
package model

import (
	"context"
	"errors"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	board "github.com/chai-rs/handdraw-server/internal/board/model"
	document "github.com/chai-rs/handdraw-server/internal/document/service"
)

// ErrImportUnavailable keeps unsupported import activation closed until its workflow exists.
var ErrImportUnavailable = errors.New("import initialization unavailable")

// CreateBoard contains normalized metadata and a supported server-owned initialization mode.
type CreateBoard struct {
	Name           string  `json:"name"`
	ProjectID      *string `json:"project_id"`
	Initialization string  `json:"initialization"`
}

// Normalize copies optional values and removes surrounding name whitespace before hashing.
func (p CreateBoard) Normalize() CreateBoard {
	p.Name = string(board.BoardName(p.Name).Normalize())
	if p.ProjectID != nil {
		v := *p.ProjectID
		p.ProjectID = &v
	}

	return p
}

// Validate requires a supported builder mode and valid optional grouping.
func (p CreateBoard) Validate() error {
	if err := board.BoardName(p.Name).Validate(); err != nil {
		return err
	}

	if p.Initialization != "empty" && p.Initialization != "get_started" && p.Initialization != "import" {
		return ErrImportUnavailable
	}

	if p.ProjectID != nil {
		if *p.ProjectID == "" {
			return board.ErrInvalidProject
		}

		return (board.ProjectAssignment{Set: true, ID: *p.ProjectID}).Validate()
	}

	return nil
}

// BoardView joins metadata with the document schema and actor's current capabilities.
type BoardView struct {
	CanInsertPremium bool                `json:"can_insert_premium"`
	Board            board.Board         `json:"-"`
	SchemaVersion    int                 `json:"document_schema_version"`
	Capabilities     access.Capabilities `json:"capabilities"`
}

// ProjectScope identifies a project's immutable parent, retaining tombstone metadata for repeated deletion.
type ProjectScope struct {
	WorkspaceID string `json:"workspace_id"`
	Deleted     bool   `json:"deleted"`
}

// Query owns the scoped application joins that do not belong in a single domain.
//
//mockery:generate: true
type Query interface {
	LockWorkspace(context.Context, string) error
	ProjectScope(context.Context, string) (ProjectScope, error)
	Document(context.Context, string) ([]byte, int, error)
	SchemaVersion(context.Context, string) (int, error)
}

// Access evaluates current membership, lifecycle and entitlement.
//
//mockery:generate: true
type Access interface {
	Resolve(context.Context, access.Target) (access.Decision, error)
	Require(context.Context, access.Target, access.Action) (access.Decision, error)
}

// Builder supplies schema-validated initial content for the board's already assigned ID.
//
//mockery:generate: true
type Builder interface {
	Build(string, string) (document.InitialDocument, error)
}

// Boards exposes domain operations while preserving server-owned initial identities.
//
//mockery:generate: true
type Boards interface {
	CreatePrepared(context.Context, board.Board, board.InitialDocument) (board.Board, error)
	Get(context.Context, string, string) (board.Board, error)
	List(context.Context, string, *string, board.PageRequest) (board.Page[board.Board], error)
	Update(context.Context, string, string, board.BoardPatch, int64) (board.Board, error)
	MarkDeleting(context.Context, string, string, int64) (board.Board, error)
}

// Projects owns project metadata and non-cascading deletion.
//
//mockery:generate: true
type Projects interface {
	Create(context.Context, board.NewProjectParams) (board.Project, error)
	Get(context.Context, string, string) (board.Project, error)
	List(context.Context, string, board.PageRequest) (board.Page[board.Project], error)
	Rename(context.Context, string, string, string, int64) (board.Project, error)
	DeleteEmpty(context.Context, string, string, int64) error
}
