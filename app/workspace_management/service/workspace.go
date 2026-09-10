// Package service coordinates workspace metadata with effective access inside one request transaction.
package service

import (
	"context"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/workspace_management/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/cursor"
)

// Service composes domain and access ports without concrete database dependencies.
type Service struct {
	workspaces model.Workspaces
	access     model.Access
}

// New binds the workspace and access services at the application boundary.
func New(workspaces model.Workspaces, access model.Access) *Service {
	return &Service{workspaces: workspaces, access: access}
}

// Page returns authorized projections plus a repository-owned continuation position.
type Page struct {
	Items []access.Decision `json:"items"`
	Next  *cursor.Position  `json:"-"`
}

// Get permits members to read metadata for subscription recovery while content remains gated.
func (s *Service) Get(ctx context.Context, id string) (access.Decision, error) {
	return s.access.Require(ctx, access.Target{WorkspaceID: id}, access.ReadMetadata)
}

// List rechecks access for each workspace before it leaves the transaction.
func (s *Service) List(ctx context.Context, p workspace.PageRequest) (Page, error) {
	page, err := s.workspaces.List(ctx, p)
	if err != nil {
		return Page{}, err
	}

	result := Page{Items: []access.Decision{}, Next: page.Next}
	for _, w := range page.Items {
		d, err := s.Get(ctx, w.ID)
		if err != nil {
			return Page{}, err
		}

		result.Items = append(result.Items, d)
	}

	return result, nil
}

// Rename requires Owner permission, performs a guarded CAS, then returns the committed-intent projection.
func (s *Service) Rename(ctx context.Context, id string, name workspace.Name, expected int64) (access.Decision, error) {
	if _, err := s.access.Require(ctx, access.Target{WorkspaceID: id}, access.RenameWorkspace); err != nil {
		return access.Decision{}, err
	}

	if _, err := s.workspaces.Rename(ctx, id, name, expected); err != nil {
		return access.Decision{}, err
	}

	return s.Get(ctx, id)
}
