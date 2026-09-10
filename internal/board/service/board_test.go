package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/internal/board/model"
	"github.com/chai-rs/handdraw-server/internal/board/model/mocks"
	"github.com/chai-rs/handdraw-server/internal/board/service"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	workspace = "ws_0ujsswThIGTUYm2K8FjOOfXtY1K"
	boardID   = "brd_0ujsswThIGTUYm2K8FjOOfXtY1K"
	projectID = "prj_0ujsswThIGTUYm2K8FjOOfXtY1K"
	userID    = "usr_0ujsswThIGTUYm2K8FjOOfXtY1K"
)

func TestUpdateDistinguishesOmittedGroupingFromExplicitUngrouping(t *testing.T) {
	repo := mocks.NewMockBoardRepository(t)
	s := service.NewBoardService(repo)
	name := "  Renamed  "
	normalized := "Renamed"
	patch := model.BoardPatch{Name: &normalized}
	repo.EXPECT().Update(mock.Anything, workspace, boardID, patch, int64(2)).Return(model.Board{}, nil).Once()
	_, err := s.Update(context.Background(), workspace, boardID, model.BoardPatch{Name: &name}, 2)
	require.NoError(t, err)
	require.Equal(t, "  Renamed  ", name)
	ungroup := model.BoardPatch{Project: model.ProjectAssignment{Set: true}}
	repo.EXPECT().Update(mock.Anything, workspace, boardID, ungroup, int64(3)).Return(model.Board{}, nil).Once()
	_, err = s.Update(context.Background(), workspace, boardID, ungroup, 3)
	require.NoError(t, err)
}

func TestInvalidMutationNeverReachesRepository(t *testing.T) {
	s := service.NewBoardService(mocks.NewMockBoardRepository(t))
	_, err := s.Update(context.Background(), workspace, boardID, model.BoardPatch{}, 1)
	require.ErrorIs(t, err, model.ErrInvalidState)
	name := "Valid"
	_, err = s.Update(context.Background(), workspace, boardID, model.BoardPatch{Name: &name}, 0)
	require.ErrorIs(t, err, model.ErrInvalidRevision)
	_, err = s.Create(context.Background(), model.NewBoardParams{}, model.InitialDocument{})
	require.ErrorIs(t, err, model.ErrInvalidState)
}

func TestCreateReturnsDatabaseStateAndPreservesInitialDocument(t *testing.T) {
	repo := mocks.NewMockBoardRepository(t)
	s := service.NewBoardService(repo)
	document := model.InitialDocument{State: []byte{0, 0}, SchemaVersion: 1}
	now := time.Now().UTC()
	expected, err := model.RehydrateBoard(model.RehydrateBoardParams{ID: boardID, WorkspaceID: workspace, Name: "C4", CreatedBy: userID, Revision: 1, Status: model.StatusActive, CreatedAt: now, UpdatedAt: now})
	require.NoError(t, err)
	repo.EXPECT().Create(mock.Anything, mock.Anything, document).Run(func(_ context.Context, value model.Board, got model.InitialDocument) {
		require.Equal(t, "C4", value.Name())
		require.Equal(t, workspace, value.WorkspaceID())
		require.Equal(t, userID, value.CreatedBy())
		require.Equal(t, model.StatusActive, value.Status())
		require.Equal(t, int64(1), value.Revision())
		require.NotEmpty(t, value.ID())
		require.Equal(t, document, got)
	}).Return(expected, nil).Once()
	actual, err := s.Create(context.Background(), model.NewBoardParams{WorkspaceID: workspace, Name: " C4 ", CreatedBy: userID, Status: model.StatusActive}, document)
	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func TestProjectDeletePreservesRepositoryConflict(t *testing.T) {
	repo := mocks.NewMockProjectRepository(t)
	s := service.NewProjectService(repo)
	repo.EXPECT().DeleteEmpty(mock.Anything, workspace, projectID, int64(1)).Return(model.ErrProjectNotEmpty).Once()
	require.ErrorIs(t, s.DeleteEmpty(context.Background(), workspace, projectID, 1), model.ErrProjectNotEmpty)
}

func TestListAppliesBoundedDefaultBeforeQuery(t *testing.T) {
	repo := mocks.NewMockProjectRepository(t)
	s := service.NewProjectService(repo)
	repo.EXPECT().List(mock.Anything, workspace, model.PageRequest{Limit: 50}).Return(model.Page[model.Project]{Items: []model.Project{}}, nil).Once()
	result, err := s.List(context.Background(), workspace, model.PageRequest{})
	require.NoError(t, err)
	require.NotNil(t, result.Items)
}
