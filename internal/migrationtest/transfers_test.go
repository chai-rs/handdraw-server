//go:build migrationtest

package migrationtest_test

import (
	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/stretchr/testify/require"
)

// TestTransferMigrationReplaysWithConstrainedOwner verifies role grants and all three new increments roll back.
func (s *migrationSuite) TestTransferMigrationReplaysWithConstrainedOwner() {
	t := s.T()
	f := s.foundationVersion(12)
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 12))
	var count int
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE n.nspname='handdraw' AND a.grantee=0 AND a.privilege_type='EXECUTE'`).Scan(t.Context(), &count))
	require.Zero(t, count)
	for _, q := range []string{"SELECT handdraw.lease_transfer(repeat('a',64))", "SELECT lease_token FROM handdraw.transfer_jobs", "UPDATE handdraw.assets SET premium=false"} {
		_, err := f.request.ExecContext(t.Context(), q)
		require.Error(t, err)
	}
	run(t, f.dir, f.dsn, "down", "3")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 9))
	run(t, f.dir, f.dsn, "up")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 12))
}
