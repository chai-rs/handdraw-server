package service_test

import (
	"testing"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	membershipmocks "github.com/chai-rs/handdraw-server/app/membership/model/mocks"
	"github.com/chai-rs/handdraw-server/app/membership/service"
	idemmocks "github.com/chai-rs/handdraw-server/internal/idempotency/model/mocks"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestNonOwnerInvitationNeverTouchesTokensOrPersistence proves the workflow denies before reserving a seat.
func TestNonOwnerInvitationNeverTouchesTokensOrPersistence(t *testing.T) {
	repository := membershipmocks.NewMockRepository(t)
	policy := membershipmocks.NewMockAccess(t)
	tokens := membershipmocks.NewMockTokens(t)
	requests := idemmocks.NewMockRepository(t)
	target := access.Target{WorkspaceID: "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv"}
	policy.EXPECT().Require(mock.Anything, target, access.ReadMetadata).Return(access.Decision{Facts: access.Facts{Member: workspace.Member{Role: workspace.Editor}}}, nil).Once()
	_, err := service.New(repository, policy, tokens, requests).Invite(t.Context(), target, workspace.InviteParams{Email: "dev@example.com", Role: workspace.Editor}, "52ef1b34-0161-49b2-8107-5adad101fb4c")
	require.ErrorIs(t, err, access.ErrDenied)
}

// TestBoardEditorInvitationIsRejectedBeforePolicyLookup preserves the confirmed Viewer-only sharing contract.
func TestBoardEditorInvitationIsRejectedBeforePolicyLookup(t *testing.T) {
	repository := membershipmocks.NewMockRepository(t)
	policy := membershipmocks.NewMockAccess(t)
	tokens := membershipmocks.NewMockTokens(t)
	requests := idemmocks.NewMockRepository(t)
	_, err := service.New(repository, policy, tokens, requests).Invite(t.Context(), access.Target{BoardID: "brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv"}, workspace.InviteParams{Email: "dev@example.com", Role: workspace.Editor}, "52ef1b34-0161-49b2-8107-5adad101fb4c")
	require.ErrorIs(t, err, workspace.ErrInvalid)
}

// TestInvitationTokensRequireTheServerSecretAndAreStableForDeliveryRetries verifies reproducibility without predictable public tokens.
func TestInvitationTokensRequireTheServerSecretAndAreStableForDeliveryRetries(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	signer, err := service.NewTokens(key)
	require.NoError(t, err)
	id := "inv_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	first, err := signer.Issue(id)
	require.NoError(t, err)
	require.NoError(t, first.Validate())
	key[0] = 'z'
	replay, err := signer.Issue(id)
	require.NoError(t, err)
	require.Equal(t, first, replay)
	otherSigner, err := service.NewTokens(key)
	require.NoError(t, err)
	other, err := otherSigner.Issue(id)
	require.NoError(t, err)
	require.NotEqual(t, first, other)
	digest, err := first.Digest()
	require.NoError(t, err)
	require.Len(t, digest, 32)
	require.NotEqual(t, string(first), string(digest))
	_, err = service.NewTokens([]byte("short"))
	require.Error(t, err)
}
