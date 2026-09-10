// Package service coordinates guarded team changes, invitation identity and effective board access.
package service

import (
	"context"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/membership/model"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/cursor"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Service applies domain validation before the SQL guards repeat authorization and seat checks.
type Service struct {
	repository model.Repository
	access     model.Access
	tokens     model.Tokens
	requests   idem.Repository
}

// New receives ports without introducing dependencies between domains.
func New(repository model.Repository, access model.Access, tokens model.Tokens, requests idem.Repository) *Service {
	return &Service{repository: repository, access: access, tokens: tokens, requests: requests}
}

func (s *Service) owner(ctx context.Context, target access.Target, editable bool) (access.Decision, error) {
	d, err := s.access.Require(ctx, target, access.ReadMetadata)
	if err != nil {
		return d, err
	}

	if d.Facts.Member.Role != workspace.Owner {
		return d, access.ErrDenied
	}

	if editable && !d.Capabilities.CanManageGuests {
		return d, access.ErrDenied
	}

	return d, nil
}

// Members returns only the roster of a workspace the actor belongs to.
func (s *Service) Members(ctx context.Context, w string, p model.PageRequest) ([]model.Member, *cursor.Position, error) {
	if err := p.Validate(workspace.UserIDPrefix); err != nil {
		return nil, nil, err
	}

	if _, err := s.access.Require(ctx, access.Target{WorkspaceID: w}, access.ReadMetadata); err != nil {
		return nil, nil, err
	}

	return s.repository.Members(ctx, w, p)
}

// ChangeMember keeps the Owner invariant and stale-revision checks on the guarded persistence path.
func (s *Service) ChangeMember(ctx context.Context, p workspace.RoleChange) (model.Member, error) {
	if err := p.Validate(); err != nil {
		return model.Member{}, err
	}

	if _, err := s.owner(ctx, access.Target{WorkspaceID: p.WorkspaceID}, false); err != nil {
		return model.Member{}, err
	}

	if err := s.repository.ChangeMember(ctx, p); err != nil {
		return model.Member{}, err
	}

	if p.Role == nil {
		return model.Member{}, nil
	}

	return s.repository.Member(ctx, p.WorkspaceID, p.UserID)
}

// Invite reserves capacity atomically and persists only the token hash; retries never disclose bearer material.
func (s *Service) Invite(ctx context.Context, target access.Target, p workspace.InviteParams, key string) (workspace.Invitation, error) {
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return workspace.Invitation{}, err
	}

	if target.BoardID != "" && p.Role != workspace.Viewer {
		return workspace.Invitation{}, workspace.ErrInvalid
	}

	d, err := s.owner(ctx, target, true)
	if err != nil {
		return workspace.Invitation{}, err
	}

	w := d.Facts.Workspace.ID
	if target.BoardID == "" && d.Facts.Workspace.Kind != "team" {
		return workspace.Invitation{}, access.ErrDenied
	}

	var board *string

	if target.BoardID != "" {
		b := target.BoardID
		board = &b
	}

	request, err := idem.New("invitation.create:"+w, key, w, struct {
		BoardID *string                `json:"board_id"`
		Params  workspace.InviteParams `json:"params"`
	}{board, p})
	if err != nil {
		return workspace.Invitation{}, err
	}

	ticket, err := s.requests.Begin(ctx, request)
	if err != nil {
		return workspace.Invitation{}, err
	}

	id := ticket.Reference
	if !ticket.Replayed {
		id, err = resourceid.New(workspace.InvitationIDPrefix)
		if err != nil {
			return workspace.Invitation{}, err
		}

		token, err := s.tokens.Issue(id)
		if err != nil {
			return workspace.Invitation{}, err
		}

		digest, err := token.Digest()
		if err != nil {
			return workspace.Invitation{}, err
		}

		id, err = s.repository.Invite(ctx, id, w, board, p, digest)
		if err != nil {
			return workspace.Invitation{}, err
		}

		if err = s.requests.Complete(ctx, ticket, id, 202); err != nil {
			return workspace.Invitation{}, err
		}
	}

	return s.repository.Invitation(ctx, id)
}

// Invitations returns Owner-visible history with expiry evaluated at read time.
func (s *Service) Invitations(ctx context.Context, w string, p model.PageRequest) ([]workspace.Invitation, *cursor.Position, error) {
	if err := p.Validate(workspace.InvitationIDPrefix); err != nil {
		return nil, nil, err
	}

	if _, err := s.owner(ctx, access.Target{WorkspaceID: w}, false); err != nil {
		return nil, nil, err
	}

	return s.repository.Invitations(ctx, w, p)
}

// Accept relies on the restricted SQL identity guard to compare the actor's current verified Auth email.
func (s *Service) Accept(ctx context.Context, token workspace.InvitationToken) (model.AccessResult, error) {
	digest, err := token.Digest()
	if err != nil {
		return model.AccessResult{}, err
	}

	return s.repository.Accept(ctx, digest)
}

// Revoke only changes a pending invitation; consumed invitations cannot restore or remove accepted access.
func (s *Service) Revoke(ctx context.Context, id string) error {
	if err := resourceid.Validate(id, workspace.InvitationIDPrefix); err != nil {
		return err
	}

	i, err := s.repository.Invitation(ctx, id)
	if err != nil {
		return err
	}

	if _, err = s.owner(ctx, access.Target{WorkspaceID: i.WorkspaceID}, false); err != nil {
		return err
	}

	return s.repository.Revoke(ctx, id)
}

// Guests lists explicit Viewer grants independently of the workspace roster.
func (s *Service) Guests(ctx context.Context, b string) ([]model.Guest, error) {
	if _, err := s.owner(ctx, access.Target{BoardID: b}, false); err != nil {
		return nil, err
	}

	return s.repository.Guests(ctx, b)
}

// RemoveGuest returns remaining membership permissions after the grant is removed.
func (s *Service) RemoveGuest(ctx context.Context, b, u string) (model.Removal, error) {
	if err := resourceid.Validate(u, workspace.UserIDPrefix); err != nil {
		return model.Removal{}, err
	}

	if _, err := s.owner(ctx, access.Target{BoardID: b}, false); err != nil {
		return model.Removal{}, err
	}

	return s.repository.RemoveGuest(ctx, b, u)
}

// SharedBoardIDs enumerates only active, currently readable explicit board grants.
func (s *Service) SharedBoardIDs(ctx context.Context, p model.PageRequest) ([]string, *cursor.Position, error) {
	if err := p.Validate(workspace.BoardIDPrefix); err != nil {
		return nil, nil, err
	}

	return s.repository.SharedBoardIDs(ctx, p)
}
