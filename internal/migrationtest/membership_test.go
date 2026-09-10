//go:build migrationtest

package migrationtest_test

import (
	"context"

	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/stretchr/testify/require"
)

// TestMembershipMigrationUsesNarrowPrivilegesAndReplaysWithoutLosingOwners exercises version nine with a constrained migrator.
func (s *migrationSuite) TestMembershipMigrationUsesNarrowPrivilegesAndReplaysWithoutLosingOwners() {
	t := s.T()
	f := s.foundationVersion(9)
	owner := f.profile(t)
	w := f.workspace(t, owner, "team")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 9))
	var count int
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE n.nspname='handdraw' AND a.grantee=0 AND a.privilege_type='EXECUTE'`).Scan(t.Context(), &count))
	require.Zero(t, count)
	for _, query := range []string{`SELECT token_hash FROM handdraw.invitations`, `SELECT email FROM auth.users`, `SELECT handdraw.member_email_matches('anything','anything')`, `INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES('anything','anything','editor')`} {
		require.Error(t, rlstx.Run(t.Context(), f.request, owner, func(ctx context.Context) error {
			tx, err := rlstx.Current(ctx)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, query)
			return err
		}))
	}
	run(t, f.dir, f.dsn, "down", "1")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 8))
	run(t, f.dir, f.dsn, "up")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 9))
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM handdraw.workspace_members WHERE workspace_id=? AND user_id=? AND role='owner'`, w, owner).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
}
