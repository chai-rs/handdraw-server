package model_test

import (
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/stretchr/testify/require"
)

// TestMembershipRequiresMatchingOwnerAndPersonalIsolation checks invariants before future membership writes are added.
func TestMembershipRequiresMatchingOwnerAndPersonalIsolation(t *testing.T) {
	now := time.Now().UTC()
	w := model.Workspace{ID: "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv", OwnerID: "usr_0ujtsYcgvSTl8PAuAdqWYSMnLOv", Kind: "team", Name: "Architecture", Lifecycle: "ready", Revision: 1, AccessRevision: 1, CreatedAt: now, UpdatedAt: now}
	for _, tc := range []struct {
		name, kind, user string
		role             model.Role
		valid            bool
	}{
		{"matching owner", "team", w.OwnerID, model.Owner, true},
		{"owner cannot be viewer", "team", w.OwnerID, model.Viewer, false},
		{"second owner", "team", "usr_0ujsswThIGTUYm2K8FjOOfXtY1K", model.Owner, false},
		{"team editor", "team", "usr_0ujsswThIGTUYm2K8FjOOfXtY1K", model.Editor, true},
		{"personal extra member", "personal", "usr_0ujsswThIGTUYm2K8FjOOfXtY1K", model.Viewer, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := w
			copy.Kind = tc.kind
			m := model.Member{WorkspaceID: w.ID, UserID: tc.user, Role: tc.role, Revision: 1}
			if tc.valid {
				require.NoError(t, m.ValidateFor(copy))
			} else {
				require.ErrorIs(t, m.ValidateFor(copy), model.ErrInvalid)
			}
		})
	}
}
