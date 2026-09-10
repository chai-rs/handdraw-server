package model

import (
	"context"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

// ProjectRepository persists metadata in the caller's actor-scoped transaction.
// Missing and inaccessible resources return ErrNotFound; mutations compare revision atomically.
//
//mockery:generate: true
type ProjectRepository interface {
	Create(context.Context, Project) (Project, error)
	Get(context.Context, string, string) (Project, error)
	List(context.Context, string, PageRequest) (Page[Project], error)
	Rename(context.Context, string, string, string, int64) (Project, error)
	DeleteEmpty(context.Context, string, string, int64) error
}

// InitialDocument is content already validated by the application content workflow.
// The metadata domain checks storage preconditions, not Yjs/schema semantics.
type InitialDocument struct {
	State         []byte `json:"-"`
	SchemaVersion int    `json:"schema_version"`
}

// ProjectAssignment distinguishes an omitted project field from explicitly removing grouping.
type ProjectAssignment struct {
	Set bool   `json:"set"`
	ID  string `json:"id"`
}

// BoardPatch changes only explicitly supplied metadata fields.
type BoardPatch struct {
	Name    *string           `json:"name,omitempty"`
	Project ProjectAssignment `json:"project"`
}

// BoardRepository owns board metadata and initial document persistence in the caller's transaction.
// MarkDeleting must be coordinated with job enqueue and room invalidation by the application.
//
//mockery:generate: true
type BoardRepository interface {
	Create(context.Context, Board, InitialDocument) (Board, error)
	Get(context.Context, string, string) (Board, error)
	List(context.Context, string, *string, PageRequest) (Page[Board], error)
	Update(context.Context, string, string, BoardPatch, int64) (Board, error)
	MarkDeleting(context.Context, string, string, int64) (Board, error)
}

// Validate checks storage preconditions after application content validation.
func (d InitialDocument) Validate() error {
	if err := valx.Struct(&d,
		valx.Field(&d.State, valx.Required),
		valx.Field(&d.SchemaVersion, valx.Required, valx.Min(1)),
	); err != nil {
		return ErrInvalidState
	}

	return nil
}

// Normalize copies a supplied name before trimming so the caller's patch is unchanged.
func (p BoardPatch) Normalize() BoardPatch {
	if p.Name != nil {
		name := string(BoardName(*p.Name).Normalize())
		p.Name = &name
	}

	return p
}

// Validate requires a supplied field and validates only the requested changes.
func (p BoardPatch) Validate() error {
	if err := valx.Var(p.Name != nil || p.Project.Set, valx.Required); err != nil {
		return ErrInvalidState
	}

	if p.Name != nil {
		if err := BoardName(*p.Name).Validate(); err != nil {
			return err
		}
	}

	return p.Project.Validate()
}

// Validate checks a project ID only when grouping is explicitly assigned.
func (p ProjectAssignment) Validate() error {
	return valx.When(p.Set && p.ID != "", valx.NewIDRule("project", ProjectIDPrefix)).Validate(p.ID)
}
