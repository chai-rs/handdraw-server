// Package service implements board-domain operations without calling other domains.
// Application workflows supply authorization and the shared transaction context.
package service

import (
	"context"

	"github.com/chai-rs/handdraw-server/internal/board/model"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

// ProjectService validates and executes project metadata operations.
type ProjectService struct{ repository model.ProjectRepository }

// NewProjectService binds project operations to their persistence interface.
func NewProjectService(repository model.ProjectRepository) *ProjectService {
	return &ProjectService{repository: repository}
}

// Create assigns identity and revision after the application verifies workspace write access.
func (s *ProjectService) Create(ctx context.Context, params model.NewProjectParams) (model.Project, error) {
	project, err := model.NewProject(params)
	if err != nil {
		return model.Project{}, err
	}

	return s.repository.Create(ctx, project)
}

// Get reads a project within its owning workspace.
func (s *ProjectService) Get(ctx context.Context, workspace, id string) (model.Project, error) {
	if err := (model.ProjectReference{WorkspaceID: workspace, ID: id}).Validate(); err != nil {
		return model.Project{}, err
	}

	return s.repository.Get(ctx, workspace, id)
}

// List returns a bounded keyset page, with no unpaged fallback.
func (s *ProjectService) List(ctx context.Context, workspace string, page model.PageRequest) (model.Page[model.Project], error) {
	if err := valx.NewIDRule("workspace", model.WorkspaceIDPrefix).Validate(workspace); err != nil {
		return model.Page[model.Project]{}, err
	}

	page, err := page.Normalize(model.ProjectIDPrefix)
	if err != nil {
		return model.Page[model.Project]{}, err
	}

	return s.repository.List(ctx, workspace, page)
}

// Rename changes the name only if expectedRevision is current.
func (s *ProjectService) Rename(ctx context.Context, workspace, id, name string, expectedRevision int64) (model.Project, error) {
	if err := (model.ProjectReference{WorkspaceID: workspace, ID: id}).Validate(); err != nil {
		return model.Project{}, err
	}

	if err := model.ExpectedRevision(expectedRevision).Validate(); err != nil {
		return model.Project{}, err
	}

	normalized := model.ProjectName(name).Normalize()
	if err := normalized.Validate(); err != nil {
		return model.Project{}, err
	}

	return s.repository.Rename(ctx, workspace, id, string(normalized), expectedRevision)
}

// DeleteEmpty soft-deletes an empty project and never cascades board deletion.
func (s *ProjectService) DeleteEmpty(ctx context.Context, workspace, id string, expectedRevision int64) error {
	if err := (model.ProjectReference{WorkspaceID: workspace, ID: id}).Validate(); err != nil {
		return err
	}

	if err := model.ExpectedRevision(expectedRevision).Validate(); err != nil {
		return err
	}

	return s.repository.DeleteEmpty(ctx, workspace, id, expectedRevision)
}
