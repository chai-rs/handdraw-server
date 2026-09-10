package service_test

import (
	"testing"

	"github.com/chai-rs/handdraw-server/app/discussion/model/mocks"
	"github.com/chai-rs/handdraw-server/app/discussion/service"
	comment "github.com/chai-rs/handdraw-server/internal/comment/model"
	document "github.com/chai-rs/handdraw-server/internal/document/model"
	documentmocks "github.com/chai-rs/handdraw-server/internal/document/model/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestCreateRejectsMissingTargetBeforeWriting preserves the stable anchor contract.
func TestCreateRejectsMissingTargetBeforeWriting(t *testing.T) {
	repository := mocks.NewMockRepository(t)
	codec := documentmocks.NewMockCodec(t)
	board := "brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	repository.EXPECT().Lock(mock.Anything, board).Return(nil).Once()
	repository.EXPECT().Document(mock.Anything, board).Return([]byte("state"), nil).Once()
	codec.EXPECT().Decode([]byte("state"), document.Validation{BoardID: board}).Return(document.Snapshot{}, nil).Once()
	_, err := service.New(repository, codec).Create(t.Context(), board, comment.Create{Anchor: comment.Anchor{Kind: "note", NoteID: "note_0ujtsYcgvSTl8PAuAdqWYSMnLOv"}, Body: "question"})
	require.ErrorIs(t, err, comment.ErrInvalid)
}

// TestRevokedAccessStopsBeforeReadingContent checks the mutation's current access lock.
func TestRevokedAccessStopsBeforeReadingContent(t *testing.T) {
	repository := mocks.NewMockRepository(t)
	codec := documentmocks.NewMockCodec(t)
	board := "brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	repository.EXPECT().Lock(mock.Anything, board).Return(comment.ErrNotFound).Once()
	_, err := service.New(repository, codec).Create(t.Context(), board, comment.Create{Anchor: comment.Anchor{Kind: "board"}, Body: "question"})
	require.ErrorIs(t, err, comment.ErrNotFound)
}
