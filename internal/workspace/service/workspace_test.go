package service_test

import (
	"testing"

	"github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/internal/workspace/model/mocks"
	"github.com/chai-rs/handdraw-server/internal/workspace/service"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestRenameNormalizesBeforePersistence preserves the exact intended name and CAS revision.
func TestRenameNormalizesBeforePersistence(t *testing.T) {
	r := mocks.NewMockRepository(t)
	id := "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	r.EXPECT().Rename(mock.Anything, id, model.Name("Architecture"), int64(7)).Return(model.Workspace{Name: "Architecture"}, nil).Once()
	w, err := service.New(r).Rename(t.Context(), id, " Architecture ", 7)
	require.NoError(t, err)
	require.Equal(t, "Architecture", w.Name)
}

// TestInvalidMutationsDoNotReachPersistence rejects malformed IDs, names and revisions before calling the port.
func TestInvalidMutationsDoNotReachPersistence(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		value    model.Name
		revision int64
	}{
		{"wrong ID", "brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv", "name", 1},
		{"empty name", "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv", " ", 1},
		{"zero revision", "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv", "name", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.New(mocks.NewMockRepository(t)).Rename(t.Context(), tc.id, tc.value, tc.revision)
			require.ErrorIs(t, err, model.ErrInvalid)
		})
	}
}
