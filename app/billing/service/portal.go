package service

import (
	"context"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// PortalService creates customer portal sessions after repository-owned authorization.
type PortalService struct {
	repository model.PortalRepository
	provider   model.PortalProvider
}

// NewPortal binds Owner authorization to the external customer-session provider.
func NewPortal(repository model.PortalRepository, provider model.PortalProvider) *PortalService {
	return &PortalService{repository: repository, provider: provider}
}

// Create checks the workspace identifier and resolves the provider customer inside the actor transaction.
func (s *PortalService) Create(ctx context.Context, workspaceID string) (string, error) {
	if resourceid.Validate(workspaceID, model.WorkspaceIDPrefix) != nil || s.repository == nil || s.provider == nil {
		return "", model.ErrInvalid
	}

	customerID, err := s.repository.PortalCustomer(ctx, workspaceID)
	if err != nil {
		return "", err
	}

	return s.provider.CreatePortal(ctx, customerID)
}
