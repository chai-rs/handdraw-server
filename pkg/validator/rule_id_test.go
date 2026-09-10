package valx_test

import (
	"strings"
	"testing"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
	"github.com/stretchr/testify/require"
)

func TestIDRuleAcceptsCanonicalResourceIDs(t *testing.T) {
	for _, prefix := range []string{"usr", "ws", "brd", "prj"} {
		t.Run(prefix, func(t *testing.T) {
			id, err := resourceid.New(prefix)
			require.NoError(t, err)
			require.NoError(t, valx.NewIDRule("resource", prefix).Validate(id))
			value := struct{ ID string }{ID: id}
			require.NoError(t, valx.Struct(&value, valx.Field(&value.ID, valx.NewIDRule("resource", prefix))))
		})
	}
}

func TestIDRuleRejectsMalformedIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"empty", ""},
		{"prefix only", "usr_"},
		{"short", "usr_1234567890"},
		{"wrong prefix", "brd_0ujtsYcgvSTl8PAuAdqWYSMnLOv"},
		{"zero", "usr_" + strings.Repeat("0", 27)},
		{"overflow", "usr_" + strings.Repeat("z", 27)},
		{"nil", nil},
		{"wrong type", 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorIs(t, valx.NewIDRule("user", "usr").Validate(tc.value), resourceid.ErrInvalid)
		})
	}
}
