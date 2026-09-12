//go:build migrationtest

package migrationtest_test

import (
	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/stretchr/testify/require"
)

// TestBillingMigrationReplaysWithConstrainedOwner checks functions, RLS and rollback without elevated runtime roles.
func (s *migrationSuite) TestBillingMigrationReplaysWithConstrainedOwner() {
	t := s.T()
	f := s.foundationVersion(13)
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 13))
	for _, q := range []string{"SELECT handdraw.lease_billing(repeat('a',64))", "SELECT * FROM handdraw.local_billing_operations", "INSERT INTO handdraw.payment_orders DEFAULT VALUES"} {
		_, err := f.request.ExecContext(t.Context(), q)
		require.Error(t, err)
	}
	run(t, f.dir, f.dsn, "down", "1")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 12))
	run(t, f.dir, f.dsn, "up")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 13))
}

// TestPolarCheckoutMigrationReplays keeps checkout state function-only and restores schema 13.
func (s *migrationSuite) TestPolarCheckoutMigrationReplays() {
	t := s.T()
	f := s.foundationVersion(14)
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 14))
	for _, q := range []string{"SELECT handdraw.polar_billing_prepare('x')", "SELECT handdraw.apply_polar_checkout('x','x','x','https://example.com')"} {
		_, err := f.request.ExecContext(t.Context(), q)
		require.Error(t, err)
	}
	run(t, f.dir, f.dsn, "down", "1")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 13))
	run(t, f.dir, f.dsn, "up")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 14))
}
