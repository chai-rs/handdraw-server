package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	accessmocks "github.com/chai-rs/handdraw-server/app/access/model/mocks"
	appmocks "github.com/chai-rs/handdraw-server/app/collaboration/model/mocks"
	"github.com/chai-rs/handdraw-server/app/collaboration/service"
	collab "github.com/chai-rs/handdraw-server/internal/collaboration/model"
	document "github.com/chai-rs/handdraw-server/internal/document/model"
	documentmocks "github.com/chai-rs/handdraw-server/internal/document/model/mocks"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const boardID = "brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv"

func principal(t *testing.T) identity.Principal {
	t.Helper()
	subject := identity.AuthSubject(uuid.NewString())
	p, err := identity.NewProfile(identity.NewProfileParams{AuthUserID: subject, DisplayName: "Developer"})
	require.NoError(t, err)
	return identity.Principal{Profile: p, Identity: identity.AuthIdentity{Subject: subject, ExpiresAt: time.Now().Add(time.Hour)}}
}

// TestApplyNeverReturnsCandidateAfterCommitFailure prevents the transport from publishing uncertain writes.
func TestApplyNeverReturnsCandidateAfterCommitFailure(t *testing.T) {
	auth := accessmocks.NewMockAuthenticator(t)
	tx := accessmocks.NewMockTransactions(t)
	policy := appmocks.NewMockAccess(t)
	docs := appmocks.NewMockDocuments(t)
	codec := documentmocks.NewMockCodec(t)
	p := principal(t)
	target := access.Target{BoardID: boardID}
	decision := access.Decision{Capabilities: access.Capabilities{CanRead: true, CanEditContent: true}}
	decision.Facts.Workspace.ID = "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	auth.EXPECT().Authenticate(mock.Anything, identity.AccessToken("token")).Return(p, nil).Once()
	uncertain := errors.New("commit response lost")
	tx.EXPECT().Run(mock.Anything, p.Profile.ID(), mock.Anything).RunAndReturn(func(ctx context.Context, _ string, fn func(context.Context) error) error {
		require.NoError(t, fn(ctx))
		return uncertain
	}).Once()
	policy.EXPECT().Require(mock.Anything, target, access.ReadContent).Return(decision, nil).Once()
	docs.EXPECT().Load(mock.Anything, boardID).Return(collab.Document{State: []byte("committed"), Revision: 8}, nil).Once()
	docs.EXPECT().Scope(mock.Anything, boardID).Return(document.Validation{BoardID: boardID}, nil).Once()
	codec.EXPECT().Apply([]byte("committed"), []byte("update"), document.Validation{BoardID: boardID}).Return([]byte("candidate"), nil).Once()
	docs.EXPECT().LockWorkspace(mock.Anything, decision.Facts.Workspace.ID).Return(nil).Once()
	policy.EXPECT().Require(mock.Anything, target, access.EditContent).Return(decision, nil).Once()
	codec.EXPECT().Decode([]byte("committed"), document.Validation{BoardID: boardID}).Return(document.Snapshot{}, nil).Once()
	codec.EXPECT().Decode([]byte("candidate"), document.Validation{BoardID: boardID}).Return(document.Snapshot{}, nil).Once()
	docs.EXPECT().Save(mock.Anything, boardID, int64(8), []byte("candidate")).Return(nil).Once()
	result, a, err := service.New(auth, tx, policy, docs, codec).Apply(t.Context(), "token", p.Profile.ID(), boardID, 8, []byte("update"))
	require.ErrorIs(t, err, uncertain)
	require.Empty(t, result)
	require.Empty(t, a)
}

// TestRefreshCannotChangeTheAuthenticatedUser prevents a socket from becoming another account mid-room.
func TestRefreshCannotChangeTheAuthenticatedUser(t *testing.T) {
	auth := accessmocks.NewMockAuthenticator(t)
	tx := accessmocks.NewMockTransactions(t)
	policy := appmocks.NewMockAccess(t)
	docs := appmocks.NewMockDocuments(t)
	codec := documentmocks.NewMockCodec(t)
	p := principal(t)
	auth.EXPECT().Authenticate(mock.Anything, identity.AccessToken("replacement")).Return(p, nil).Once()
	_, err := service.New(auth, tx, policy, docs, codec).Check(t.Context(), "replacement", "usr_0ujtsYcgvSTl8PAuAdqWYSMnLOv", boardID)
	require.ErrorIs(t, err, collab.ErrSession)
}
