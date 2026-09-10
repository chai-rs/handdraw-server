package service_test

import (
	"testing"

	"github.com/chai-rs/handdraw-server/internal/document/infra/ygo"
	"github.com/chai-rs/handdraw-server/internal/document/model"
	"github.com/chai-rs/handdraw-server/internal/document/service"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/stretchr/testify/require"
)

// TestInitialDocumentOpensWithoutClientBootstrap verifies the encoded schema and separate page identity.
func TestInitialDocumentOpensWithoutClientBootstrap(t *testing.T) {
	board, err := resourceid.New(model.BoardIDPrefix)
	require.NoError(t, err)
	for _, mode := range []string{"empty", "get_started"} {
		t.Run(mode, func(t *testing.T) {
			codec := ygo.Codec{}
			initial, err := service.NewInitialBuilder(codec).Build(board, mode)
			require.NoError(t, err)
			require.NoError(t, resourceid.Validate(initial.PageID, model.PageIDPrefix))
			require.NotEqual(t, board, initial.PageID)
			snapshot, err := codec.Decode(initial.State, model.Validation{BoardID: board})
			require.NoError(t, err)
			require.Equal(t, []string{initial.PageID}, snapshot.PageOrder)
			if mode == "get_started" {
				require.NotEmpty(t, snapshot.Notes)
				require.NotEmpty(t, snapshot.Pages[initial.PageID].Scene.Elements)
			}
		})
	}
}
