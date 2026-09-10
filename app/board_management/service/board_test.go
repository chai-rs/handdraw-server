package service_test

import (
	"testing"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/board_management/model"
	boardmocks "github.com/chai-rs/handdraw-server/app/board_management/model/mocks"
	"github.com/chai-rs/handdraw-server/app/board_management/service"
	idemmocks "github.com/chai-rs/handdraw-server/internal/idempotency/model/mocks"
	jobmocks "github.com/chai-rs/handdraw-server/internal/job/model/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDeniedBoardCreationNeverBuildsOrWritesContent(t *testing.T) {
	boards := boardmocks.NewMockBoards(t)
	projects := boardmocks.NewMockProjects(t)
	builder := boardmocks.NewMockBuilder(t)
	policy := boardmocks.NewMockAccess(t)
	query := boardmocks.NewMockQuery(t)
	keys := idemmocks.NewMockRepository(t)
	jobs := jobmocks.NewMockRepository(t)
	const workspace = "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	policy.EXPECT().Require(mock.Anything, access.Target{WorkspaceID: workspace}, access.EditContent).Return(access.Decision{}, access.ErrDenied).Once()
	_, err := service.New(boards, projects, builder, policy, query, keys, jobs).Create(t.Context(), workspace, "52ef1b34-0161-49b2-8107-5adad101fb4c", model.CreateBoard{Name: "Architecture", Initialization: "empty"})
	require.ErrorIs(t, err, access.ErrDenied)
}
