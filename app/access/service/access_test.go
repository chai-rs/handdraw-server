package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/access/model/mocks"
	"github.com/chai-rs/handdraw-server/app/access/service"
	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	billing "github.com/chai-rs/handdraw-server/internal/billing/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	owner  = "usr_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	member = "usr_0ujsswThIGTUYm2K8FjOOfXtY1K"
	ws     = "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
)

func facts(role workspace.Role, mode, kind string) model.Facts {
	now := time.Now().UTC()
	user := member
	if role == workspace.Owner {
		user = owner
	}
	plan := "team"
	if kind == "personal" {
		plan = "cloud"
	}
	return model.Facts{Workspace: workspace.Workspace{ID: ws, OwnerID: owner, Kind: kind, Name: "Architecture", Lifecycle: "ready", Revision: 1, AccessRevision: 1, CreatedAt: now, UpdatedAt: now}, Member: workspace.Member{WorkspaceID: ws, UserID: user, Role: role, Revision: 1}, Entitlement: billing.Entitlement{Mode: mode, Plan: plan}, Usage: asset.Usage{Revision: 1}}
}

// TestCapabilitiesKeepRoleAndEntitlementSeparate covers read-only retention, Viewer commenting and premium insertion.
func TestCapabilitiesKeepRoleAndEntitlementSeparate(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		role                         workspace.Role
		mode, kind                   string
		read, edit, comment, premium bool
	}{
		{"team Owner", workspace.Owner, "editable", "team", true, true, true, true},
		{"team Editor", workspace.Editor, "editable", "team", true, true, true, true},
		{"paid Viewer", workspace.Viewer, "editable", "team", true, false, true, false},
		{"personal Owner", workspace.Owner, "editable", "personal", true, true, true, true},
		{"retained Editor", workspace.Editor, "read_only", "team", true, false, false, false},
		{"retained Owner", workspace.Owner, "read_only", "personal", true, false, false, false},
		{"unpaid", workspace.Owner, "unavailable", "personal", false, false, false, false},
		{"purging", workspace.Owner, "purging", "team", false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := mocks.NewMockRepository(t)
			target := model.Target{WorkspaceID: ws}
			repo.EXPECT().Load(mock.Anything, target).Return(facts(tc.role, tc.mode, tc.kind), nil).Once()
			d, err := service.New(repo).Resolve(t.Context(), target)
			require.NoError(t, err)
			require.Equal(t, tc.read, d.Capabilities.CanRead)
			require.Equal(t, tc.read, d.Capabilities.CanExport)
			require.Equal(t, tc.edit, d.Capabilities.CanEditContent)
			require.Equal(t, tc.comment, d.Capabilities.CanComment)
			require.Equal(t, tc.premium, d.CanInsertPremium)
		})
	}
}

// TestActionsReloadPolicyRatherThanTrustingPreviousCapabilities simulates a revoked writable entitlement.
func TestActionsReloadPolicyRatherThanTrustingPreviousCapabilities(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	target := model.Target{WorkspaceID: ws}
	repo.EXPECT().Load(mock.Anything, target).Return(facts(workspace.Owner, "editable", "team"), nil).Once()
	repo.EXPECT().Load(mock.Anything, target).Return(facts(workspace.Owner, "read_only", "team"), nil).Once()
	s := service.New(repo)
	_, err := s.Require(t.Context(), target, model.EditContent)
	require.NoError(t, err)
	_, err = s.Require(t.Context(), target, model.EditContent)
	require.ErrorIs(t, err, model.ErrDenied)
}

// TestUnsupportedFactsCannotGrantEdit rejects unknown roles, inconsistent Owners and resource states.
func TestUnsupportedFactsCannotGrantEdit(t *testing.T) {
	for _, role := range []workspace.Role{"admin", workspace.Owner} {
		t.Run(string(role), func(t *testing.T) {
			repo := mocks.NewMockRepository(t)
			f := facts(role, "editable", "team")
			f.Member.UserID = member
			repo.EXPECT().Load(mock.Anything, model.Target{WorkspaceID: ws}).Return(f, nil)
			_, err := service.New(repo).Resolve(t.Context(), model.Target{WorkspaceID: ws})
			require.ErrorIs(t, err, model.ErrUnavailable)
		})
	}
}

// TestSessionDoesNotOpenTransactionForFailedIdentity proves authentication precedes every scoped callback.
func TestSessionDoesNotOpenTransactionForFailedIdentity(t *testing.T) {
	auth := mocks.NewMockAuthenticator(t)
	tx := mocks.NewMockTransactions(t)
	auth.EXPECT().Authenticate(mock.Anything, identity.AccessToken("invalid")).Return(identity.Principal{}, identity.ErrUnauthenticated).Once()
	err := service.NewSession(auth, tx).Run(t.Context(), "invalid", func(context.Context) error { t.Fatal("unauthenticated callback ran"); return nil })
	require.ErrorIs(t, err, identity.ErrUnauthenticated)
}
