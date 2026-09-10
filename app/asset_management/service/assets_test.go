package service_test

import (
	"errors"
	"testing"
	"time"

	appmocks "github.com/chai-rs/handdraw-server/app/asset_management/model/mocks"
	"github.com/chai-rs/handdraw-server/app/asset_management/service"
	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	assetmocks "github.com/chai-rs/handdraw-server/internal/asset/model/mocks"
	idemmocks "github.com/chai-rs/handdraw-server/internal/idempotency/model/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestStorageFailureCannotPublishOrChargeAnAsset leaves the reservation eligible for recovery.
func TestStorageFailureCannotPublishOrChargeAnAsset(t *testing.T) {
	repo := appmocks.NewMockRepository(t)
	storage := assetmocks.NewMockStorage(t)
	idem := idemmocks.NewMockRepository(t)
	id := "ast_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	a := asset.Asset{ID: id, Status: "pending", ExpiresAt: time.Now().Add(time.Hour)}
	repo.EXPECT().Lock(mock.Anything, id, true).Return(nil).Once()
	repo.EXPECT().Get(mock.Anything, id).Return(a, nil).Once()
	unavailable := errors.New("Garage disconnected")
	storage.EXPECT().Finalize(mock.Anything, a).Return(unavailable).Once()
	_, err := service.New(repo, storage, idem).Complete(t.Context(), id)
	require.ErrorIs(t, err, unavailable)
}

// TestAlreadyFinalizedRetryNeverWritesStorageOrUsage returns the existing publication after fresh authorization.
func TestAlreadyFinalizedRetryNeverWritesStorageOrUsage(t *testing.T) {
	repo := appmocks.NewMockRepository(t)
	storage := assetmocks.NewMockStorage(t)
	idem := idemmocks.NewMockRepository(t)
	id := "ast_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	a := asset.Asset{ID: id, Status: "available"}
	repo.EXPECT().Lock(mock.Anything, id, true).Return(nil).Once()
	repo.EXPECT().Get(mock.Anything, id).Return(a, nil).Once()
	result, err := service.New(repo, storage, idem).Complete(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, a, result)
}
