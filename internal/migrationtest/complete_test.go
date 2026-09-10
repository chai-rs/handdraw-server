//go:build migrationtest

package migrationtest_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

func (f foundation) worker(t *testing.T, role string) *bun.DB {
	t.Helper()
	login := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := f.admin.ExecContext(t.Context(), "CREATE ROLE ? LOGIN PASSWORD 'local_test_only' IN ROLE ?", bun.Ident(login), bun.Ident(role))
	require.NoError(t, err)
	u, err := url.Parse(f.dsn)
	require.NoError(t, err)
	u.User = url.UserPassword(login, "local_test_only")
	q := u.Query()
	q.Del("x-migrations-table")
	u.RawQuery = q.Encode()
	return openDB(t, u.String())
}

func (f foundation) trial(t *testing.T, w string) {
	t.Helper()
	_, err := f.migrator.ExecContext(t.Context(), `INSERT INTO handdraw.subscriptions(workspace_id,plan,billing_interval,status,paid_seats,trial_ends_at)
 VALUES (?,'team','month','trialing',5,clock_timestamp()+interval '7 days')`, w)
	require.NoError(t, err)
}

func (f foundation) readable(t *testing.T, actor, w string, write bool) bool {
	t.Helper()
	var result bool
	require.NoError(t, rlstx.Run(t.Context(), f.request, actor, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		fn := "can_read_workspace"
		if write {
			fn = "can_write_workspace"
		}
		return tx.NewRaw("SELECT ?(?)", bun.Safe("handdraw."+fn), w).Scan(ctx, &result)
	}))
	return result
}

// TestCompleteFoundationEntitlementWindows proves unpaid, expired and over-capacity states fail closed.
func (s *migrationSuite) TestCompleteFoundationEntitlementWindows() {
	t := s.T()
	f := s.foundationVersion(5)
	owner, viewer := f.profile(t), f.profile(t)
	w := f.workspace(t, owner, "team")
	require.False(t, f.readable(t, owner, w, false))
	require.False(t, f.readable(t, owner, w, true))
	f.trial(t, w)
	require.True(t, f.readable(t, owner, w, true))
	_, err := f.migrator.ExecContext(t.Context(), "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'viewer')", w, viewer)
	require.NoError(t, err)
	require.True(t, f.readable(t, viewer, w, false))
	require.False(t, f.readable(t, viewer, w, true))
	cases := []struct {
		name, change string
		read, write  bool
	}{
		{"trial expired", "trial_ends_at=clock_timestamp()-interval '1 hour',access_expires_at=clock_timestamp()-interval '1 hour'", true, false},
		{"retention expired", "status='ended',access_expires_at=clock_timestamp()-interval '91 days'", false, false},
		{"paid active", "status='active',last_successful_payment_at=clock_timestamp(),paid_through_at=clock_timestamp()+interval '1 month'", true, true},
		{"grace", "status='grace_period',renewal_obligation_id='test-renewal',renewal_failure_at=clock_timestamp(),grace_ends_at=clock_timestamp()+interval '6 days',access_expires_at=clock_timestamp()+interval '6 days'", true, true},
		{"invalid grace", "grace_ends_at=clock_timestamp()+interval '8 days'", true, false},
		{"suspended", "status='suspended'", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.migrator.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET "+tc.change+" WHERE workspace_id=?", w)
			require.NoError(t, err)
			require.Equal(t, tc.read, f.readable(t, owner, w, false))
			require.Equal(t, tc.write, f.readable(t, owner, w, true))
		})
	}
	_, err = f.migrator.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='active',paid_seats=1 WHERE workspace_id=?", w)
	require.NoError(t, err)
	_, err = f.migrator.ExecContext(t.Context(), "UPDATE handdraw.workspace_members SET role='editor',revision=revision+1 WHERE workspace_id=? AND user_id=?", w, viewer)
	require.NoError(t, err)
	require.True(t, f.readable(t, owner, w, false))
	require.False(t, f.readable(t, owner, w, true))
}

// TestCompleteFoundationWorkerBoundaries limits billing/quota workers to their own projection operations.
func (s *migrationSuite) TestCompleteFoundationWorkerBoundaries() {
	t := s.T()
	f := s.foundationVersion(5)
	owner := f.profile(t)
	w := f.workspace(t, owner, "team")
	f.trial(t, w)
	quota := f.worker(t, "handdraw_quota_worker")
	billing := f.worker(t, "handdraw_billing_worker")
	var revision int64
	require.NoError(t, quota.NewRaw("SELECT handdraw.adjust_usage(?,0,100,1)", w).Scan(t.Context(), &revision))
	require.EqualValues(t, 2, revision)
	for _, q := range []string{"SELECT handdraw.adjust_usage(?,0,100,1)", "SELECT handdraw.adjust_usage(?,0,50000000001,2)", "SELECT handdraw.adjust_usage(?,-1,0,2)"} {
		_, err := quota.ExecContext(t.Context(), q, w)
		require.Error(t, err)
	}
	require.NoError(t, quota.NewRaw("SELECT handdraw.adjust_usage(?,100,-100,2)", w).Scan(t.Context(), &revision))
	require.EqualValues(t, 3, revision)
	for _, db := range []*bun.DB{quota, billing, f.request} {
		for _, q := range []string{"SELECT auth_user_id FROM handdraw.profiles", "DELETE FROM handdraw.workspaces", "TRUNCATE handdraw.board_documents", "SET ROLE handdraw_access_owner"} {
			_, err := db.ExecContext(t.Context(), q)
			require.Error(t, err)
		}
	}
	_, err := f.request.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='active'")
	require.Error(t, err)
	_, err = quota.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='active'")
	require.Error(t, err)
	_, err = billing.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET plan='cloud',paid_seats=1 WHERE workspace_id=?", w)
	require.Error(t, err)
	_, err = billing.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='ended',access_expires_at=clock_timestamp(),revision=revision+1 WHERE workspace_id=?", w)
	require.NoError(t, err)
	require.False(t, f.readable(t, owner, w, true))
}

// TestCompleteFoundationIdempotencyIsolation binds records to actor, operation and current workspace membership.
func (s *migrationSuite) TestCompleteFoundationIdempotencyIsolation() {
	t := s.T()
	f := s.foundationVersion(5)
	owner, other := f.profile(t), f.profile(t)
	w := f.workspace(t, owner, "team")
	id, err := resourceid.New("idem")
	require.NoError(t, err)
	key := uuid.NewString()
	require.NoError(t, rlstx.Run(t.Context(), f.request, owner, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO handdraw.idempotency_records(id,actor_user_id,workspace_id,operation,idempotency_key,request_hash,status,expires_at)
 VALUES (?,?,?,'create-board',?::uuid,decode(repeat('00',32),'hex'),'processing',clock_timestamp()+interval '24 hours')`, id, owner, w, key)
		return err
	}))
	require.NoError(t, rlstx.Run(t.Context(), f.request, other, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		var count int
		err = tx.NewRaw("SELECT count(*) FROM handdraw.idempotency_records").Scan(ctx, &count)
		require.Zero(t, count)
		return err
	}))
	require.NoError(t, rlstx.Run(t.Context(), f.request, owner, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE handdraw.idempotency_records SET status='completed',result_ref='{}',response_status=201 WHERE id=?", id)
		return err
	}))
	gc := f.worker(t, "handdraw_idempotency_gc")
	result, err := gc.ExecContext(t.Context(), "DELETE FROM handdraw.idempotency_records")
	require.NoError(t, err)
	count, err := result.RowsAffected()
	require.NoError(t, err)
	require.Zero(t, count)
	_, err = f.migrator.ExecContext(t.Context(), "UPDATE handdraw.idempotency_records SET created_at=clock_timestamp()-interval '2 days',expires_at=clock_timestamp()-interval '1 day'")
	require.NoError(t, err)
	result, err = gc.ExecContext(t.Context(), "DELETE FROM handdraw.idempotency_records")
	require.NoError(t, err)
	count, err = result.RowsAffected()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

// TestCompleteFoundationReadinessAndReplay rejects dirty/incompatible versions and replays the complete foundation.
func (s *migrationSuite) TestCompleteFoundationReadinessAndReplay() {
	t := s.T()
	f := s.foundationVersion(5)
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 5))
	require.ErrorIs(t, bunx.CheckSchema(t.Context(), f.request, 4), bunx.ErrSchemaUnavailable)
	_, err := f.admin.ExecContext(t.Context(), "UPDATE public.handdraw_schema_migrations SET dirty=true")
	require.NoError(t, err)
	require.ErrorIs(t, bunx.CheckSchema(t.Context(), f.request, 5), bunx.ErrSchemaUnavailable)
	_, err = f.admin.ExecContext(t.Context(), "UPDATE public.handdraw_schema_migrations SET dirty=false")
	require.NoError(t, err)
	run(t, f.dir, f.dsn, "down", "3")
	owner := f.profile(t)
	w := f.workspace(t, owner, "team")
	run(t, f.dir, f.dsn, "up")
	var count int
	require.NoError(t, f.migrator.NewRaw("SELECT count(*) FROM handdraw.workspace_usage WHERE workspace_id=?", w).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 5))
	run(t, f.dir, f.dsn, "down", "5")
	run(t, f.dir, f.dsn, "up")
	require.NoError(t, f.admin.NewRaw("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='handdraw' AND c.relkind='r' AND c.relrowsecurity AND c.relforcerowsecurity").Scan(t.Context(), &count))
	require.Equal(t, 9, count)
}

// TestCompleteSecurityCatalog verifies the full foundation retains restricted roles and definer ownership.
func (s *migrationSuite) TestCompleteSecurityCatalog() {
	t := s.T()
	f := s.foundationVersion(5)
	var count int
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_roles WHERE rolname IN
 ('handdraw_request','handdraw_identity_resolver','handdraw_identity_owner','handdraw_access_owner','handdraw_billing_worker','handdraw_quota_worker','handdraw_idempotency_gc')
 AND NOT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb AND NOT rolcreaterole`).Scan(t.Context(), &count))
	require.Equal(t, 7, count)
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
 WHERE n.nspname='handdraw' AND a.grantee=0 AND a.privilege_type='EXECUTE'`).Scan(t.Context(), &count))
	require.Zero(t, count)
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='handdraw' AND p.prosecdef
 AND (r.rolname NOT IN ('handdraw_identity_owner','handdraw_access_owner') OR NOT coalesce(p.proconfig @> ARRAY['search_path=""']::text[],false))`).Scan(t.Context(), &count))
	require.Zero(t, count)
}

// TestAccessIncrementReplaysWithConstrainedMigrator verifies projection grants and a reversible metadata-only upgrade.
func (s *migrationSuite) TestAccessIncrementReplaysWithConstrainedMigrator() {
	t := s.T()
	f := s.foundationVersion(6)
	owner, viewer := f.profile(t), f.profile(t)
	w := f.workspace(t, owner, "team")
	f.trial(t, w)
	_, err := f.migrator.ExecContext(t.Context(), "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'viewer')", w, viewer)
	require.NoError(t, err)
	require.NoError(t, rlstx.Run(t.Context(), f.request, viewer, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		var mode string
		err = tx.NewRaw("SELECT mode FROM handdraw.access_entitlement(?)", w).Scan(ctx, &mode)
		require.Equal(t, "editable", mode)
		return err
	}))
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 6))
	run(t, f.dir, f.dsn, "down", "1")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 5))
	run(t, f.dir, f.dsn, "up")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 6))
	require.NoError(t, rlstx.Run(t.Context(), f.request, owner, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE handdraw.workspaces SET name='Retained',revision=revision+1 WHERE id=?", w)
		return err
	}))
	var count int
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a
 WHERE n.nspname='handdraw' AND a.grantee=0 AND a.privilege_type='EXECUTE'`).Scan(t.Context(), &count))
	require.Zero(t, count)
}

// TestOnboardingCleanupUpgradeReplay retains bootstrapped workspaces while replaying the two new increments under a constrained migrator.
func (s *migrationSuite) TestOnboardingCleanupUpgradeReplay() {
	t := s.T()
	f := s.foundationVersion(8)
	actor := f.profile(t)
	w, err := resourceid.New("ws")
	require.NoError(t, err)
	require.NoError(t, rlstx.Run(t.Context(), f.request, actor, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "SELECT handdraw.bootstrap_workspace(?,?,'personal')", w, "Bootstrap")
		return err
	}))
	require.False(t, f.readable(t, actor, w, true))
	run(t, f.dir, f.dsn, "down", "2")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 6))
	run(t, f.dir, f.dsn, "up")
	require.NoError(t, bunx.CheckSchema(t.Context(), f.request, 8))
	var count int
	require.NoError(t, f.admin.NewRaw("SELECT count(*) FROM handdraw.workspaces WHERE id=?", w).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace CROSS JOIN LATERAL aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE n.nspname='handdraw' AND a.grantee=0 AND a.privilege_type='EXECUTE'`).Scan(t.Context(), &count))
	require.Zero(t, count)
	require.Error(t, rlstx.Run(t.Context(), f.request, actor, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "SELECT handdraw.run_board_cleanup()")
		return err
	}))
}
