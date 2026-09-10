// Package service validates workspace operations without looking up other domains.
package service

import (
	"context"

	"github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Service receives the workspace persistence port from application wiring.
type Service struct{ repository model.Repository }

// New binds the persistence adapter.
func New(repository model.Repository) *Service { return &Service{repository: repository} }

// Get returns visible workspace metadata under the existing transaction scope.
func (s *Service) Get(ctx context.Context, id string) (model.Workspace, error) {
	if resourceid.Validate(id, model.WorkspaceIDPrefix) != nil {
		return model.Workspace{}, model.ErrInvalid
	}

	return s.repository.Get(ctx, id)
}

// List accepts only a bounded, validated keyset query.
func (s *Service) List(ctx context.Context, p model.PageRequest) (model.Page, error) {
	if err := p.Validate(); err != nil {
		return model.Page{}, err
	}

	return s.repository.List(ctx, p)
}

// Rename normalizes input and requires a positive expected revision before persistence.
func (s *Service) Rename(ctx context.Context, id string, name model.Name, revision int64) (model.Workspace, error) {
	name = name.Normalize()
	if resourceid.Validate(id, model.WorkspaceIDPrefix) != nil || revision < 1 || name.Validate() != nil {
		return model.Workspace{}, model.ErrInvalid
	}

	return s.repository.Rename(ctx, id, name, revision)
}
