package cursor_test

import (
	"strings"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/pkg/cursor"
	"github.com/stretchr/testify/require"
)

// TestCursorCannotCrossQueryScope validates signature integrity and each query dimension.
func TestCursorCannotCrossQueryScope(t *testing.T) {
	codec, err := cursor.New([]byte(strings.Repeat("k", 32)))
	require.NoError(t, err)
	scope := cursor.Scope{ActorID: "usr_0ujtsYcgvSTl8PAuAdqWYSMnLOv", WorkspaceID: "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv", ResourcePrefix: "brd", Order: "updated_at_desc_id_desc"}
	position := cursor.Position{UpdatedAt: time.Now().UTC(), ID: "brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv"}
	token, err := codec.Encode(scope, position)
	require.NoError(t, err)
	actual, err := codec.Decode(token, scope)
	require.NoError(t, err)
	require.Equal(t, position, actual)
	for _, change := range []func(*cursor.Scope){func(s *cursor.Scope) { s.ActorID = "usr_0ujsswThIGTUYm2K8FjOOfXtY1K" }, func(s *cursor.Scope) { s.WorkspaceID = "ws_0ujsswThIGTUYm2K8FjOOfXtY1K" }, func(s *cursor.Scope) { s.Filter = "ungrouped" }, func(s *cursor.Scope) { s.ResourcePrefix = "prj" }, func(s *cursor.Scope) { s.Order = "reverse" }} {
		changed := scope
		change(&changed)
		_, err := codec.Decode(token, changed)
		require.ErrorIs(t, err, cursor.ErrInvalid)
	}
	_, err = codec.Decode(token+"x", scope)
	require.ErrorIs(t, err, cursor.ErrInvalid)
}
