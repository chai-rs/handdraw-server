package model

import (
	"strings"
	"time"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Project is immutable project metadata; content and authorization are coordinated by app workflows.
type Project struct {
	id          string
	workspaceID string
	name        string
	createdBy   string
	revision    int64
	createdAt   time.Time
	updatedAt   time.Time
}

// NewProjectParams contains caller-owned metadata after application authorization.
type NewProjectParams struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	CreatedBy   string `json:"created_by"`
}

// NewProject validates metadata and assigns an ID; persistence supplies timestamps.
func NewProject(params NewProjectParams) (Project, error) {
	params = params.Normalize()
	if err := params.Validate(); err != nil {
		return Project{}, err
	}

	id, err := resourceid.New(ProjectIDPrefix)
	if err != nil {
		return Project{}, err
	}

	return Project{id: id, workspaceID: params.WorkspaceID, name: params.Name, createdBy: params.CreatedBy, revision: 1}, nil
}

// RehydrateProjectParams describes a live persisted project row.
type RehydrateProjectParams struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	CreatedBy   string    `json:"created_by"`
	Revision    int64     `json:"revision,string"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RehydrateProject rejects corrupt persisted identities, revisions, or metadata.
func RehydrateProject(params RehydrateProjectParams) (Project, error) {
	if err := params.Validate(); err != nil {
		return Project{}, err
	}

	return Project{id: params.ID, workspaceID: params.WorkspaceID, name: params.Name, createdBy: params.CreatedBy, revision: params.Revision, createdAt: params.CreatedAt, updatedAt: params.UpdatedAt}, nil
}

// ID returns the resource identifier.
func (v Project) ID() string { return v.id }

// WorkspaceID returns the immutable workspace owner.
func (v Project) WorkspaceID() string { return v.workspaceID }

// Name returns the normalized display name.
func (v Project) Name() string { return v.name }

// CreatedBy returns the original author's stable profile ID.
func (v Project) CreatedBy() string { return v.createdBy }

// Revision returns the metadata revision, independent of document revision.
func (v Project) Revision() int64 { return v.revision }

// CreatedAt returns the persistence creation timestamp.
func (v Project) CreatedAt() time.Time { return v.createdAt }

// UpdatedAt returns the latest metadata mutation timestamp.
func (v Project) UpdatedAt() time.Time { return v.updatedAt }

// ProjectMaxNameRunes bounds a project's normalized name.
const ProjectMaxNameRunes = 200

// ProjectName owns the naming invariant for a project.
type ProjectName string

// Normalize returns a name with surrounding whitespace removed.
func (n ProjectName) Normalize() ProjectName { return ProjectName(strings.TrimSpace(string(n))) }

// Validate checks normalized text against this entity's name rules.
func (n ProjectName) Validate() error {
	if err := valx.Var(string(n.Normalize()), valx.Required, valx.RuneLength(1, ProjectMaxNameRunes), valx.UTF8Text); err != nil {
		return ErrInvalidName
	}

	return nil
}

// Normalize returns creation parameters without modifying the caller's value.
func (p NewProjectParams) Normalize() NewProjectParams {
	p.Name = string(ProjectName(p.Name).Normalize())
	return p
}

// Validate checks creation input; persistence owns timestamps and the initial revision.
func (p NewProjectParams) Validate() error {
	if err := ProjectName(p.Name).Validate(); err != nil {
		return err
	}

	if err := valx.Struct(&p,
		valx.Field(&p.WorkspaceID, valx.NewIDRule("workspace", WorkspaceIDPrefix)),
		valx.Field(&p.CreatedBy, valx.NewIDRule("user", UserIDPrefix)),
	); err != nil {
		return resourceid.ErrInvalid
	}

	return nil
}

// Validate rejects corrupt persisted metadata without normalizing database values.
func (p RehydrateProjectParams) Validate() error {
	if err := valx.Struct(&p,
		valx.Field(&p.ID, valx.NewIDRule("project", ProjectIDPrefix)),
		valx.Field(&p.WorkspaceID, valx.NewIDRule("workspace", WorkspaceIDPrefix)),
		valx.Field(&p.CreatedBy, valx.NewIDRule("user", UserIDPrefix)),
	); err != nil {
		return resourceid.ErrInvalid
	}

	if err := ProjectName(p.Name).Validate(); err != nil {
		return err
	}

	if err := valx.Struct(&p,
		valx.Field(&p.Name, valx.In(string(ProjectName(p.Name).Normalize()))),
		valx.Field(&p.Revision, valx.Required, valx.Min(int64(1))),
		valx.Field(&p.CreatedAt, valx.Required),
		valx.Field(&p.UpdatedAt, valx.Required, valx.TimeGTE(p.CreatedAt)),
	); err != nil {
		return ErrInvalidState
	}

	return nil
}

// ProjectReference identifies a project within its immutable workspace.
type ProjectReference struct {
	WorkspaceID string `json:"workspace_id"`
	ID          string `json:"id"`
}

// Validate requires both identifiers to have the correct resource kind.
func (p ProjectReference) Validate() error {
	if err := valx.Struct(&p,
		valx.Field(&p.WorkspaceID, valx.NewIDRule("workspace", WorkspaceIDPrefix)),
		valx.Field(&p.ID, valx.NewIDRule("project", ProjectIDPrefix)),
	); err != nil {
		return resourceid.ErrInvalid
	}

	return nil
}
