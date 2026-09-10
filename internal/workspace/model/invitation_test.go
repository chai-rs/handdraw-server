package model_test

import (
	"strings"
	"testing"

	"github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/stretchr/testify/require"
)

// TestInvitationParamsRejectUnsafeRecipientAndRoleInput keeps recipient normalization separate from validation.
func TestInvitationParamsRejectUnsafeRecipientAndRoleInput(t *testing.T) {
	for _, tc := range []struct {
		name, email string
		role        model.Role
		valid       bool
	}{
		{"viewer", "dev@example.com", model.Viewer, true},
		{"editor", "dev@example.com", model.Editor, true},
		{"owner", "dev@example.com", model.Owner, false},
		{"invalid email", "not-an-email", model.Viewer, false},
		{"noncanonical", " Dev@Example.com ", model.Viewer, false},
		{"long email", strings.Repeat("a", 250) + "@example.com", model.Viewer, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (model.InviteParams{Email: tc.email, Role: tc.role}).Validate()
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, model.ErrInvalid)
			}
		})
	}
	require.Equal(t, "dev@example.com", (model.InviteParams{Email: " Dev@Example.com "}).Normalize().Email)
}

// TestInvitationTokenRejectsNoncanonicalSecrets prevents accepting short or alternate bearer encodings.
func TestInvitationTokenRejectsNoncanonicalSecrets(t *testing.T) {
	for _, value := range []string{"", strings.Repeat("a", 32), strings.Repeat("a", 43) + "=", "inv_0ujtsYcgvSTl8PAuAdqWYSMnLOv", strings.Repeat("a", 42) + "!"} {
		t.Run(value, func(t *testing.T) { require.Error(t, model.InvitationToken(value).Validate()) })
	}
}
