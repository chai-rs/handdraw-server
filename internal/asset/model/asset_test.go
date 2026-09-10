package model_test

import (
	"strings"
	"testing"

	"github.com/chai-rs/handdraw-server/internal/asset/model"
	"github.com/stretchr/testify/require"
)

// TestReservationValidationSeparatesAttachmentsAndImportBudgets prevents content-type and size bypasses.
func TestReservationValidationSeparatesAttachmentsAndImportBudgets(t *testing.T) {
	for _, tc := range []struct {
		name, purpose, mime string
		size                int64
		valid               bool
	}{
		{"image", "attachment", "image/png", 20_000_000, true},
		{"oversize image", "attachment", "image/png", 20_000_001, false},
		{"bounded source", "import_source", "application/json", 64_000_000, true},
		{"oversize source", "import_source", "application/json", 64_000_001, false},
		{"json disguised as attachment", "attachment", "application/json", 20, false},
		{"HTML rejected", "attachment", "text/html", 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (model.Reserve{Purpose: tc.purpose, MIME: tc.mime, Size: tc.size, SHA256: strings.Repeat("a", 64)}).Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, model.ErrInvalid)
			}
		})
	}
}
