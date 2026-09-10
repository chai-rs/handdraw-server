package service_test

import (
	"testing"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/onboarding/model"
	onboardingmocks "github.com/chai-rs/handdraw-server/app/onboarding/model/mocks"
	"github.com/chai-rs/handdraw-server/app/onboarding/service"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	idemmocks "github.com/chai-rs/handdraw-server/internal/idempotency/model/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestReplayReloadsCurrentAccessWithoutCreatingAnything(t *testing.T) {
	repository := onboardingmocks.NewMockRepository(t)
	keys := idemmocks.NewMockRepository(t)
	policy := onboardingmocks.NewMockAccess(t)
	const key = "52ef1b34-0161-49b2-8107-5adad101fb4c"
	const workspace = "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	request, err := idem.New("workspace.create", key, "", model.Params{Name: "Personal", Kind: "personal"})
	require.NoError(t, err)
	keys.EXPECT().Begin(mock.Anything, request).Return(idem.Ticket{Reference: workspace, Replayed: true}, nil).Once()
	policy.EXPECT().Require(mock.Anything, access.Target{WorkspaceID: workspace}, access.ReadMetadata).Return(access.Decision{}, access.ErrNotFound).Once()
	_, err = service.New(repository, keys, policy).Create(t.Context(), " Personal ", "personal", key)
	require.ErrorIs(t, err, access.ErrNotFound)
}
