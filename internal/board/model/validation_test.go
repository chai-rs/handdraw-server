package model_test

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/internal/board/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/stretchr/testify/require"
)

const (
	workspace = "ws_0ujsswThIGTUYm2K8FjOOfXtY1K"
	user      = "usr_0ujsswThIGTUYm2K8FjOOfXtY1K"
)

func TestNamesRejectInvalidOrUnboundedInput(t *testing.T) {
	for _, name := range []string{"", "   ", strings.Repeat("ก", 201), "null\x00byte", string([]byte{0xff})} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, model.BoardName(name).Validate(), model.ErrInvalidName)
			require.ErrorIs(t, model.ProjectName(name).Validate(), model.ErrInvalidName)
		})
	}
}

func TestProjectCreationNormalizesNameAndReservesDatabaseFields(t *testing.T) {
	p, err := model.NewProject(model.NewProjectParams{WorkspaceID: workspace, CreatedBy: user, Name: "  Allocation  "})
	require.NoError(t, err)
	require.Equal(t, "Allocation", p.Name())
	require.Equal(t, int64(1), p.Revision())
	require.NoError(t, resourceid.Validate(p.ID(), "prj"))
	require.True(t, p.CreatedAt().IsZero())
	require.Equal(t, workspace, p.WorkspaceID())
	require.Equal(t, user, p.CreatedBy())
}

func TestBoardCreationRejectsDeletingStateAndMalformedProject(t *testing.T) {
	params := model.NewBoardParams{WorkspaceID: workspace, CreatedBy: user, Name: "C4", Status: model.StatusDeleting}
	_, err := model.NewBoard(params)
	require.ErrorIs(t, err, model.ErrInvalidState)
	params.Status = model.StatusActive
	params.ProjectID = workspace
	_, err = model.NewBoard(params)
	require.ErrorIs(t, err, resourceid.ErrInvalid)
}

func TestRehydrateRejectsInconsistentPersistedMetadata(t *testing.T) {
	now := time.Now().UTC()
	params := model.RehydrateProjectParams{ID: "prj_0ujsswThIGTUYm2K8FjOOfXtY1K", WorkspaceID: workspace, CreatedBy: user, Name: "C4", Revision: 1, CreatedAt: now, UpdatedAt: now.Add(-time.Second)}
	_, err := model.RehydrateProject(params)
	require.ErrorIs(t, err, model.ErrInvalidState)
	params.UpdatedAt = now
	params.Revision = 0
	_, err = model.RehydrateProject(params)
	require.ErrorIs(t, err, model.ErrInvalidState)
}

func TestMutationsRejectInvalidRevisions(t *testing.T) {
	for _, revision := range []int64{-1, 0, math.MaxInt64} {
		t.Run(strconv.FormatInt(revision, 10), func(t *testing.T) {
			require.ErrorIs(t, model.ExpectedRevision(revision).Validate(), model.ErrInvalidRevision)
		})
	}
}

func TestPageRequiresBoundedLimitsAndCorrectResourcePosition(t *testing.T) {
	page, err := (model.PageRequest{}).Normalize("prj")
	require.NoError(t, err)
	require.Equal(t, 50, page.Limit)
	for _, limit := range []int{-1, 101, 100000} {
		_, err = (model.PageRequest{Limit: limit}).Normalize("prj")
		require.ErrorIs(t, err, model.ErrInvalidPage)
	}
	_, err = (model.PageRequest{After: &model.Position{UpdatedAt: time.Now(), ID: "brd_0ujsswThIGTUYm2K8FjOOfXtY1K"}}).Normalize("prj")
	require.ErrorIs(t, err, model.ErrInvalidPage)
}

func TestBoardParamsValidateCreationStatesAndReferences(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   model.Status
		project string
		want    error
	}{
		{"active ungrouped", model.StatusActive, "", nil},
		{"initializing grouped", model.StatusInitializing, "prj_0ujsswThIGTUYm2K8FjOOfXtY1K", nil},
		{"missing state", "", "", model.ErrInvalidState},
		{"deleting state", model.StatusDeleting, "", model.ErrInvalidState},
		{"wrong reference kind", model.StatusActive, workspace, resourceid.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := model.NewBoardParams{WorkspaceID: workspace, CreatedBy: user, Name: " C4 ", Status: tc.state, ProjectID: tc.project}
			original := params
			require.ErrorIs(t, params.Validate(), tc.want)
			require.Equal(t, original, params)
		})
	}
}

func TestRehydrateValidationPreservesCanonicalPersistedNames(t *testing.T) {
	now := time.Now().UTC()
	params := model.RehydrateBoardParams{ID: "brd_0ujsswThIGTUYm2K8FjOOfXtY1K", WorkspaceID: workspace, CreatedBy: user, Name: "C4", Revision: math.MaxInt64, CreatedAt: now, UpdatedAt: now, Status: model.StatusDeleting}
	require.NoError(t, params.Validate())
	require.ErrorIs(t, model.ExpectedRevision(params.Revision).Validate(), model.ErrInvalidRevision)
	params.Name = " C4 "
	require.ErrorIs(t, params.Validate(), model.ErrInvalidState)
	require.Equal(t, " C4 ", params.Name)
	params.Name = "C4"
	params.UpdatedAt = time.Time{}
	require.ErrorIs(t, params.Validate(), model.ErrInvalidState)
}

func TestPageNormalizationCopiesTheCursorAndDoesNotMutateInput(t *testing.T) {
	after := model.Position{UpdatedAt: time.Now().UTC(), ID: "brd_0ujsswThIGTUYm2K8FjOOfXtY1K"}
	input := model.PageRequest{After: &after}
	require.ErrorIs(t, input.Validate(model.BoardIDPrefix), model.ErrInvalidPage)
	normalized, err := input.Normalize(model.BoardIDPrefix)
	require.NoError(t, err)
	require.Zero(t, input.Limit)
	require.Equal(t, 50, normalized.Limit)
	normalized.After.ID = "changed"
	require.Equal(t, "brd_0ujsswThIGTUYm2K8FjOOfXtY1K", after.ID)
}

func TestNamesAcceptTheUnicodeBoundary(t *testing.T) {
	name := " " + strings.Repeat("ก", 200) + " "
	require.NoError(t, model.BoardName(name).Validate())
	require.NoError(t, model.ProjectName(name).Validate())
}
