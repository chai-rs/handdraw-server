package main

import (
	"context"
	"testing"
	"time"

	configx "github.com/chai-rs/handdraw-server/pkg/config"
	"github.com/stretchr/testify/require"
)

// These tests use the command package because configuration and bootstrap are private wiring.
func TestDisabledIdentityDoesNotRequireProviderOrDatabase(t *testing.T) {
	t.Setenv("APP_IDENTITY_ENABLED", "false")
	t.Setenv("APP_WORKSPACE_ENABLED", "false")
	t.Setenv("APP_IDENTITY_DATABASE_URL", "")
	t.Setenv("APP_IDENTITY_SUPABASE_URL", "")
	t.Setenv("APP_IDENTITY_PUBLISHABLE_KEY", "")
	conf, err := configx.New[configuration]("APP")
	require.NoError(t, err)
	require.False(t, conf.Identity.Enabled)
	require.Equal(t, "authenticated", conf.Identity.Audience)
	require.Equal(t, 5*time.Second, conf.Identity.Timeout)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.NoError(t, run(ctx, *conf))
}

func TestEnabledIdentityRequiresValidConfigurationBeforeListening(t *testing.T) {
	err := run(t.Context(), configuration{Identity: identityConfig{Enabled: true}})
	require.ErrorContains(t, err, "Supabase")
	err = run(t.Context(), configuration{Identity: identityConfig{Enabled: true, SupabaseURL: "https://example.supabase.co", PublishableKey: "test-key"}})
	require.ErrorContains(t, err, "PostgreSQL")
}

// TestWorkspaceRoutesRequireVerifiedIdentity rejects enabling a request pool without authentication.
func TestWorkspaceRoutesRequireVerifiedIdentity(t *testing.T) {
	err := run(t.Context(), configuration{Workspace: workspaceConfig{Enabled: true}})
	require.ErrorContains(t, err, "require identity")
}

// TestMembershipRequiresBoardAndIndependentTokenKey fails before opening connections with invalid wiring.
func TestMembershipRequiresBoardAndIndependentTokenKey(t *testing.T) {
	err := run(t.Context(), configuration{Membership: membershipConfig{Enabled: true}})
	require.ErrorContains(t, err, "require board")
	err = run(t.Context(), configuration{Membership: membershipConfig{Enabled: true, TokenKey: "same-key"}, Workspace: workspaceConfig{CursorKey: "same-key"}, Board: boardConfig{Enabled: true}})
	require.ErrorContains(t, err, "independent")
	err = run(t.Context(), configuration{Membership: membershipConfig{Enabled: true, TokenKey: "short"}, Board: boardConfig{Enabled: true}})
	require.ErrorContains(t, err, "32 bytes")
}
