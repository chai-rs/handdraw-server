package valx_test

import (
	"testing"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"
	"github.com/stretchr/testify/require"
)

func TestUTF8TextValidatesEncodingWithoutImposingDomainLengthRules(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		valid bool
	}{
		{"empty allowed by text rule", "", true},
		{"unicode", "ระบบ 🧭", true},
		{"line breaks", "first\nsecond", true},
		{"nul byte", "bad\x00text", false},
		{"invalid UTF8", string([]byte{0xff}), false},
		{"wrong type", 42, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := valx.UTF8Text.Validate(tc.value)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
