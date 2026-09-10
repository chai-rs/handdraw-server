package fx_test

import (
	"testing"

	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/stretchr/testify/require"
)

// TestIfMatchUsesStrongCanonicalInt64Revisions prevents ambiguous metadata writes.
func TestIfMatchUsesStrongCanonicalInt64Revisions(t *testing.T) {
	for _, tc := range []struct {
		value    string
		revision int64
		valid    bool
	}{{`"1"`, 1, true}, {`"9223372036854775807"`, 9223372036854775807, true}, {"", 0, false}, {`W/"1"`, 0, false}, {`"01"`, 0, false}, {`"0"`, 0, false}, {`"1","2"`, 0, false}, {`"9223372036854775808"`, 0, false}} {
		t.Run(tc.value, func(t *testing.T) {
			value, err := fx.ParseIfMatch(tc.value)
			if !tc.valid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.revision, value)
			encoded, err := fx.StrongETag(value)
			require.NoError(t, err)
			require.Equal(t, tc.value, encoded)
		})
	}
}
