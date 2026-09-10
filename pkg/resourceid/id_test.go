package resourceid_test

import (
	"strings"
	"testing"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/stretchr/testify/require"
)

func TestValidateRejectsMalformedOrWrongResourceIDs(t *testing.T) {
	for _, value := range []string{"", "brd_" + strings.Repeat("0", 27), "brd_" + strings.Repeat("z", 27), "ws_0ujsswThIGTUYm2K8FjOOfXtY1K", " brd_0ujsswThIGTUYm2K8FjOOfXtY1K", "brd_0ujsswThIGTUYm2K8FjOOfXtY1K ", "brd_0ujsswThIGTUYm2K8FjOOfXtY1!"} {
		t.Run(value, func(t *testing.T) { require.ErrorIs(t, resourceid.Validate(value, "brd"), resourceid.ErrInvalid) })
	}
}

func TestNewProducesDistinctCanonicalIDs(t *testing.T) {
	first, err := resourceid.New("brd")
	require.NoError(t, err)
	second, err := resourceid.New("brd")
	require.NoError(t, err)
	require.NotEqual(t, first, second)
	require.NoError(t, resourceid.Validate(first, "brd"))
	require.NoError(t, resourceid.Validate("brd_0ujsswThIGTUYm2K8FjOOfXtY1K", "brd"))
	_, err = resourceid.New("unregistered")
	require.ErrorIs(t, err, resourceid.ErrInvalid)
}
