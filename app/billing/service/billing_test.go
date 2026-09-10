package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	"github.com/chai-rs/handdraw-server/app/billing/model/mocks"
	"github.com/chai-rs/handdraw-server/app/billing/service"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestWorkerProviderFailureNeverAppliesOrManufacturesPayment(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	provider := mocks.NewMockProvider(t)
	task := model.Task{ID: "intent", Token: "lease"}
	failure := errors.New("provider unavailable")
	repo.EXPECT().Lease(mock.Anything, mock.Anything).Return(task, nil).Once()
	provider.EXPECT().Observe(mock.Anything, task.ID).Return(model.Snapshot{}, failure).Once()
	_, err := service.New(repo, provider).RunOne(t.Context())
	require.ErrorIs(t, err, failure)
}

func TestWorkerUsesDurableOperationAndRejectsMalformedObservation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int64
		want    error
	}{{"missing version", 0, model.ErrInvalid}, {"negative version", -1, model.ErrInvalid}, {"valid observation", 2, nil}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockRepository(t)
			provider := mocks.NewMockProvider(t)
			task := model.Task{ID: "intent", Token: "lease"}
			state := model.Snapshot{Version: tc.version, Status: "pending"}
			repo.EXPECT().Lease(mock.Anything, mock.Anything).Return(task, nil).Once()
			provider.EXPECT().Observe(mock.Anything, task.ID).Return(state, nil).Once()
			if tc.want == nil {
				repo.EXPECT().Apply(mock.Anything, task, state).Return(nil).Once()
				repo.EXPECT().Maintain(mock.Anything).Return(nil).Once()
			}
			_, err := service.New(repo, provider).RunOne(context.Background())
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestInvalidRequestDoesNotReachRepository(t *testing.T) {
	for _, tc := range []struct {
		name, w, action, key string
		p                    model.Command
	}{{name: "wrong scope", w: "brd_x", action: "quote"}, {name: "unknown action", w: "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv", action: "settle"}, {name: "external redirect", w: "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv", action: "checkout", p: model.Command{ReturnPath: "https://example.com"}}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockRepository(t)
			_, err := service.New(repo, nil).Request(t.Context(), tc.w, tc.action, tc.key, tc.p)
			require.ErrorIs(t, err, model.ErrInvalid)
		})
	}
}
