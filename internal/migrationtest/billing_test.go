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
