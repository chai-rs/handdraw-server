package model

import (
	"strings"
	"time"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Board is immutable board metadata; content and authorization are coordinated by app workflows.
type Board struct {
	id          string
	workspaceID string
	name        string
	createdBy   string
	revision    int64
	createdAt   time.Time
	updatedAt   time.Time
	projectID   string
	status      Status
}

// NewBoardParams contains caller-owned metadata after application authorization.
type NewBoardParams struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	CreatedBy   string `json:"created_by"`
	ProjectID   string `json:"project_id"`
	Status      Status `json:"status"`
}

// NewBoard validates metadata and assigns an ID; persistence supplies timestamps.
func NewBoard(params NewBoardParams) (Board, error) {
	params = params.Normalize()
	if err := params.Validate(); err != nil {
		return Board{}, err
	}

	id, err := resourceid.New(BoardIDPrefix)
	if err != nil {
		return Board{}, err
	}

	return Board{id: id, workspaceID: params.WorkspaceID, name: params.Name, createdBy: params.CreatedBy, revision: 1, projectID: params.ProjectID, status: params.Status}, nil
}

// RehydrateBoardParams describes a live persisted board row.
type RehydrateBoardParams struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	CreatedBy   string    `json:"created_by"`
	Revision    int64     `json:"revision,string"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ProjectID   string    `json:"project_id"`
	Status      Status    `json:"status"`
}

// RehydrateBoard rejects corrupt persisted identities, revisions, or metadata.
func RehydrateBoard(params RehydrateBoardParams) (Board, error) {
	if err := params.Validate(); err != nil {
		return Board{}, err
	}

	return Board{id: params.ID, workspaceID: params.WorkspaceID, name: params.Name, createdBy: params.CreatedBy, revision: params.Revision, createdAt: params.CreatedAt, updatedAt: params.UpdatedAt, projectID: params.ProjectID, status: params.Status}, nil
}

// ID returns the resource identifier.
func (v Board) ID() string { return v.id }

// WorkspaceID returns the immutable workspace owner.
func (v Board) WorkspaceID() string { return v.workspaceID }

// Name returns the normalized display name.
func (v Board) Name() string { return v.name }

// CreatedBy returns the original author's stable profile ID.
func (v Board) CreatedBy() string { return v.createdBy }

// Revision returns the metadata revision, independent of document revision.
func (v Board) Revision() int64 { return v.revision }

// CreatedAt returns the persistence creation timestamp.
func (v Board) CreatedAt() time.Time { return v.createdAt }

// UpdatedAt returns the latest metadata mutation timestamp.
func (v Board) UpdatedAt() time.Time { return v.updatedAt }

// ProjectID returns the optional parent project; an empty string means ungrouped.
func (v Board) ProjectID() string { return v.projectID }

// Status returns the board lifecycle used to gate application operations.
func (v Board) Status() Status { return v.status }

// BoardMaxNameRunes bounds a board's normalized name.
const BoardMaxNameRunes = 200

// BoardName owns the naming invariant for a board.
type BoardName string

// Normalize returns a name with surrounding whitespace removed.
func (n BoardName) Normalize() BoardName { return BoardName(strings.TrimSpace(string(n))) }

// Validate checks normalized text against this entity's name rules.
func (n BoardName) Validate() error {
	if err := valx.Var(string(n.Normalize()), valx.Required, valx.RuneLength(1, BoardMaxNameRunes), valx.UTF8Text); err != nil {
		return ErrInvalidName
	}

	return nil
}

// Normalize returns creation parameters without modifying the caller's value.
func (p NewBoardParams) Normalize() NewBoardParams {
	p.Name = string(BoardName(p.Name).Normalize())
	return p
}

// Validate checks creation input; persistence owns timestamps and the initial revision.
func (p NewBoardParams) Validate() error {
	if err := BoardName(p.Name).Validate(); err != nil {
		return err
	}

	if err := valx.Struct(&p,
		valx.Field(&p.WorkspaceID, valx.NewIDRule("workspace", WorkspaceIDPrefix)),
		valx.Field(&p.CreatedBy, valx.NewIDRule("user", UserIDPrefix)),
		valx.Field(&p.ProjectID, valx.When(p.ProjectID != "", valx.NewIDRule("project", ProjectIDPrefix))),
	); err != nil {
		return resourceid.ErrInvalid
	}

	if err := valx.Var(p.Status, valx.Required, valx.In(StatusActive, StatusInitializing)); err != nil {
		return ErrInvalidState
	}

	return nil
}

// Validate rejects corrupt persisted metadata without normalizing database values.
func (p RehydrateBoardParams) Validate() error {
	if err := valx.Struct(&p,
		valx.Field(&p.ID, valx.NewIDRule("board", BoardIDPrefix)),
		valx.Field(&p.WorkspaceID, valx.NewIDRule("workspace", WorkspaceIDPrefix)),
		valx.Field(&p.CreatedBy, valx.NewIDRule("user", UserIDPrefix)),
		valx.Field(&p.ProjectID, valx.When(p.ProjectID != "", valx.NewIDRule("project", ProjectIDPrefix))),
	); err != nil {
		return resourceid.ErrInvalid
	}

	if err := BoardName(p.Name).Validate(); err != nil {
		return err
	}

	if err := valx.Struct(&p,
		valx.Field(&p.Name, valx.In(string(BoardName(p.Name).Normalize()))),
		valx.Field(&p.Revision, valx.Required, valx.Min(int64(1))),
		valx.Field(&p.CreatedAt, valx.Required),
		valx.Field(&p.UpdatedAt, valx.Required, valx.TimeGTE(p.CreatedAt)),
		valx.Field(&p.Status, valx.Required, valx.In(StatusActive, StatusInitializing, StatusDeleting)),
	); err != nil {
		return ErrInvalidState
	}

	return nil
}

// BoardReference identifies a board within its immutable workspace.
type BoardReference struct {
	WorkspaceID string `json:"workspace_id"`
	ID          string `json:"id"`
}

// Validate requires both identifiers to have the correct resource kind.
func (p BoardReference) Validate() error {
	if err := valx.Struct(&p,
		valx.Field(&p.WorkspaceID, valx.NewIDRule("workspace", WorkspaceIDPrefix)),
		valx.Field(&p.ID, valx.NewIDRule("board", BoardIDPrefix)),
	); err != nil {
		return resourceid.ErrInvalid
	}

	return nil
}
