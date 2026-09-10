// Package service evaluates cross-domain access and enters request transactions after authentication.
package service

import (
	"context"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"

	"github.com/chai-rs/handdraw-server/app/access/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
)

// Service evaluates uncached access facts through its application query port.
type Service struct{ repository model.Repository }

// New binds scoped policy persistence.
func New(repository model.Repository) *Service { return &Service{repository: repository} }

// Resolve refuses corrupt facts and keeps premium insertion separate from read/export permission.
func (s *Service) Resolve(ctx context.Context, target model.Target) (model.Decision, error) {
	if err := target.Validate(); err != nil {
		return model.Decision{}, err
	}

	f, err := s.repository.Load(ctx, target)
	if err != nil {
		return model.Decision{}, err
	}

	validMember := f.Member.ValidateFor(f.Workspace) == nil
	if f.Source == "board_grant" {
		validMember = target.BoardID != "" && f.Workspace.Validate() == nil && f.Member.WorkspaceID == f.Workspace.ID && f.Member.Role == workspace.Viewer && resourceid.Validate(f.Member.UserID, workspace.UserIDPrefix) == nil && f.Member.Revision > 0
	}

	if !validMember || f.Usage.Validate() != nil {
		return model.Decision{}, model.ErrUnavailable
	}

	if target.WorkspaceID != "" && f.Workspace.ID != target.WorkspaceID {
		return model.Decision{}, model.ErrUnavailable
	}

	switch f.Entitlement.Mode {
	case "editable", "read_only", "unavailable", "purging":
	default:
		return model.Decision{}, model.ErrUnavailable
	}

	live := f.Workspace.Lifecycle == "ready" && (target.BoardID == "" || f.BoardStatus == "active")
	read := live && f.Entitlement.Readable()
	editable := read && f.Entitlement.Editable()
	caps := model.Capabilities{CanRead: read, CanEditContent: editable && f.Member.Role.CanEditContent(), CanComment: editable, CanExport: read, CanManageGuests: editable && f.Member.Role == workspace.Owner}
	premium := caps.CanEditContent && ((f.Workspace.Kind == "team" && f.Entitlement.Plan == "team") || (f.Workspace.Kind == "personal" && f.Entitlement.Plan == "cloud" && f.Member.Role == workspace.Owner))

	return model.Decision{Facts: f, Capabilities: caps, CanInsertPremium: premium}, nil
}

// Require authorizes an operation from freshly resolved facts, not previously returned capabilities.
func (s *Service) Require(ctx context.Context, target model.Target, action model.Action) (model.Decision, error) {
	d, err := s.Resolve(ctx, target)
	if err != nil {
		return d, err
	}

	allowed := false

	switch action {
	case model.ReadMetadata:
		allowed = target.BoardID == "" || d.Capabilities.CanRead
	case model.ReadContent:
		allowed = d.Capabilities.CanRead
	case model.EditContent:
		allowed = d.Capabilities.CanEditContent
	case model.RenameWorkspace:
		allowed = target.BoardID == "" && d.Facts.Member.Role == workspace.Owner && d.Capabilities.CanEditContent
	}

	if !allowed {
		return model.Decision{}, model.ErrDenied
	}

	return d, nil
}

// Session owns the verified identity → scoped transaction boundary for application handlers.
type Session struct {
	auth         model.Authenticator
	transactions model.Transactions
}

// NewSession receives authentication and transaction ports from cmd/app.
func NewSession(auth model.Authenticator, transactions model.Transactions) *Session {
	return &Session{auth: auth, transactions: transactions}
}

// Run verifies identity before opening a transaction; callbacks must finish before the HTTP response is written.
func (s *Session) Run(ctx context.Context, token identity.AccessToken, fn func(context.Context) error) error {
	p, err := s.auth.Authenticate(ctx, token)
	if err != nil {
		return err
	}

	if p.Profile.AuthUserID() != string(p.Identity.Subject) || p.Identity.Validate() != nil {
		return identity.ErrUnauthenticated
	}

	return s.transactions.Run(ctx, p.Profile.ID(), fn)
}
