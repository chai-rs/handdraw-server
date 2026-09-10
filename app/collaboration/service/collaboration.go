// Package service authenticates and commits collaboration through the cross-domain access boundary.
package service

import (
	"bytes"
	"context"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/collaboration/model"
	collab "github.com/chai-rs/handdraw-server/internal/collaboration/model"
	document "github.com/chai-rs/handdraw-server/internal/document/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Service owns authentication, semantic validation and commit, never socket publication.
type Service struct {
	auth         access.Authenticator
	transactions access.Transactions
	access       model.Access
	documents    model.Documents
	codec        document.Codec
}

var _ collab.Backend = (*Service)(nil)

// New composes identity, policy, content and the single-authority transaction boundary.
func New(auth access.Authenticator, tx access.Transactions, policy model.Access, documents model.Documents, codec document.Codec) *Service {
	return &Service{auth: auth, transactions: tx, access: policy, documents: documents, codec: codec}
}

func (s *Service) run(ctx context.Context, token, user, board string, fn func(context.Context, access.Decision) error) (collab.Access, error) {
	if resourceid.Validate(board, collab.BoardIDPrefix) != nil {
		return collab.Access{}, collab.ErrSession
	}

	p, err := s.auth.Authenticate(ctx, identity.AccessToken(token))
	if err != nil {
		return collab.Access{}, err
	}

	if p.Identity.Validate() != nil || p.Profile.AuthUserID() != string(p.Identity.Subject) || (user != "" && user != p.Profile.ID()) {
		return collab.Access{}, collab.ErrSession
	}

	var result collab.Access

	err = s.transactions.Run(ctx, p.Profile.ID(), func(ctx context.Context) error {
		d, err := s.access.Require(ctx, access.Target{BoardID: board}, access.ReadContent)
		if err != nil {
			return err
		}

		result = collab.Access{UserID: p.Profile.ID(), Capabilities: collab.Capabilities(d.Capabilities), IdleSeconds: 7200}
		if d.Facts.Entitlement.Plan == "team" {
			result.IdleSeconds = 14400
		}

		return fn(ctx, d)
	})
	if err != nil {
		return collab.Access{}, err
	}

	return result, nil
}

// Check pins token refresh to the original user and reloads current read permission.
func (s *Service) Check(ctx context.Context, token, user, board string) (collab.Access, error) {
	return s.run(ctx, token, user, board, func(context.Context, access.Decision) error { return nil })
}

// Load returns state only after the authenticated read transaction succeeds.
func (s *Service) Load(ctx context.Context, token, user, board string) (collab.Document, collab.Access, error) {
	var doc collab.Document

	a, err := s.run(ctx, token, user, board, func(ctx context.Context, _ access.Decision) error {
		var err error

		doc, err = s.documents.Load(ctx, board)

		return err
	})
	if err != nil {
		return collab.Document{}, collab.Access{}, err
	}

	return doc, a, nil
}

// Apply validates isolated CRDT history and returns it only after PostgreSQL confirms commit.
func (s *Service) Apply(ctx context.Context, token, user, board string, revision int64, update []byte) (collab.Document, collab.Access, error) {
	var doc collab.Document

	a, err := s.run(ctx, token, user, board, func(ctx context.Context, d access.Decision) error {
		var err error

		doc, err = s.documents.Load(ctx, board)
		if err != nil {
			return err
		}

		if doc.Revision != revision {
			return collab.ErrConflict
		}

		candidate, err := s.codec.Apply(doc.State, update, document.Validation{BoardID: board})
		if err != nil {
			return collab.ErrRejected
		}

		if bytes.Equal(candidate, doc.State) {
			return nil
		}

		if !d.Capabilities.CanEditContent {
			return collab.ErrRejected
		}

		if err = s.documents.LockWorkspace(ctx, d.Facts.Workspace.ID); err != nil {
			return err
		}

		if d, err = s.access.Require(ctx, access.Target{BoardID: board}, access.EditContent); err != nil {
			return err
		}

		before, decodeErr := s.codec.Decode(doc.State, document.Validation{BoardID: board})
		if decodeErr != nil {
			return collab.ErrRejected
		}

		after, decodeErr := s.codec.Decode(candidate, document.Validation{BoardID: board})
		if decodeErr != nil || after.ValidatePremiumTransition(before, document.Validation{BoardID: board}, d.CanInsertPremium) != nil {
			return collab.ErrRejected
		}

		if err = s.documents.Save(ctx, board, revision, candidate); err != nil {
			return err
		}

		doc = collab.Document{State: candidate, Revision: revision + 1}

		return nil
	})
	if err != nil {
		return collab.Document{}, collab.Access{}, err
	}

	return doc, a, nil
}
