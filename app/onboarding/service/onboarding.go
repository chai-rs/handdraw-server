// Package service coordinates idempotent workspace bootstrap without granting paid access.
package service

import (
	"context"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/onboarding/model"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Service composes bootstrap persistence, deduplication and fresh access through ports.
type Service struct {
	repository model.Repository
	keys       idem.Repository
	access     model.Access
}

// New binds transaction-scoped dependencies at application startup.
func New(repository model.Repository, keys idem.Repository, access model.Access) *Service {
	return &Service{repository: repository, keys: keys, access: access}
}

// Create completes metadata and Owner atomically and reloads current capabilities on replay.
func (s *Service) Create(ctx context.Context, name, kind, key string) (access.Decision, error) {
	p := (model.Params{Name: name, Kind: kind}).Normalize()
	if err := p.Validate(); err != nil {
		return access.Decision{}, err
	}

	request, err := idem.New("workspace.create", key, "", p)
	if err != nil {
		return access.Decision{}, err
	}

	ticket, err := s.keys.Begin(ctx, request)
	if err != nil {
		return access.Decision{}, err
	}

	id := ticket.Reference
	if !ticket.Replayed {
		id, err = resourceid.New(workspace.WorkspaceIDPrefix)
		if err != nil {
			return access.Decision{}, err
		}

		if err = s.repository.Create(ctx, id, p); err != nil {
			return access.Decision{}, err
		}

		if err = s.keys.Complete(ctx, ticket, id, 201); err != nil {
			return access.Decision{}, err
		}
	}

	return s.access.Require(ctx, access.Target{WorkspaceID: id}, access.ReadMetadata)
}
