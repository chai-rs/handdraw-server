//go:build migrationtest

package migrationtest_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

type foundation struct {
	dir      string
	dsn      string
	admin    *bun.DB
	migrator *bun.DB
	request  *bun.DB
}

func (s *migrationSuite) foundation() foundation {
	return s.foundationVersion(2)
}

func (s *migrationSuite) foundationVersion(version int) foundation {
	t := s.T()
	s.serial++
	name := fmt.Sprintf("foundation_%d", s.serial)
	migrator := name + "_migrator"
	login := name + "_request"
	_, err := s.admin.ExecContext(t.Context(), "CREATE ROLE ? LOGIN PASSWORD 'local_test_only' CREATEROLE NOSUPERUSER NOBYPASSRLS NOCREATEDB", bun.Ident(migrator))
	require.NoError(t, err)
	_, err = s.admin.ExecContext(t.Context(), "CREATE DATABASE ? OWNER ?", bun.Ident(name), bun.Ident(migrator))
	require.NoError(t, err)
	u := *s.baseURL
	u.Path = "/" + name
	admin := openDB(t, u.String())
	// Supabase owns Auth. Give the migrator only the privileges it must delegate to the resolver definer.
	_, err = admin.ExecContext(t.Context(), "CREATE SCHEMA auth; CREATE TABLE auth.users(id uuid PRIMARY KEY, email text, email_confirmed_at timestamptz)")
	require.NoError(t, err)
	_, err = admin.ExecContext(t.Context(), "GRANT USAGE ON SCHEMA auth TO ? WITH GRANT OPTION", bun.Ident(migrator))
	require.NoError(t, err)
	_, err = admin.ExecContext(t.Context(), "GRANT SELECT (id,email,email_confirmed_at), UPDATE (id), REFERENCES (id) ON auth.users TO ? WITH GRANT OPTION", bun.Ident(migrator))
	require.NoError(t, err)
	u.User = url.UserPassword(migrator, "local_test_only")
	migrationDB := openDB(t, u.String())
	query := u.Query()
	query.Set("search_path", "public")
	query.Set("x-migrations-table", "handdraw_schema_migrations")
	u.RawQuery = query.Encode()
	dir := wrapperDir(t)
	files, err := filepath.Glob("../../migrations/*.sql")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, path := range files {
		var fileVersion int
		_, err := fmt.Sscanf(filepath.Base(path), "%d_", &fileVersion)
		require.NoError(t, err)
		if fileVersion > version {
			continue
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		write(t, filepath.Join(dir, "migrations", filepath.Base(path)), string(data))
	}
	run(t, dir, u.String(), "up")
	_, err = admin.ExecContext(t.Context(), "CREATE ROLE ? LOGIN PASSWORD 'local_test_only' IN ROLE handdraw_request", bun.Ident(login))
	require.NoError(t, err)
	requestURL := *s.baseURL
	requestURL.Path = "/" + name
	requestURL.User = url.UserPassword(login, "local_test_only")
	request := openDB(t, requestURL.String())
	t.Cleanup(func() { run(t, dir, u.String(), "down", fmt.Sprint(version)) })
	return foundation{dir: dir, dsn: u.String(), admin: admin, migrator: migrationDB, request: request}
}

func (f foundation) profile(t *testing.T) string {
	t.Helper()
	subject := uuid.NewString()
	_, err := f.admin.ExecContext(t.Context(), "INSERT INTO auth.users VALUES (?::uuid)", subject)
	require.NoError(t, err)
	id, err := resourceid.New("usr")
	require.NoError(t, err)
	var resolved string
	require.NoError(t, f.migrator.NewRaw("SELECT id FROM handdraw.resolve_profile(?, ?::uuid, 'Developer')", id, subject).Scan(t.Context(), &resolved))
	require.Equal(t, id, resolved)
	return id
}

func (f foundation) workspace(t *testing.T, owner, kind string) string {
	t.Helper()
	id, err := resourceid.New("ws")
	require.NoError(t, err)
	tx, err := f.migrator.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.ExecContext(t.Context(), "INSERT INTO handdraw.workspaces(id,kind,name,owner_user_id) VALUES (?,?,'Architecture',?)", id, kind, owner)
	require.NoError(t, err)
	_, err = tx.ExecContext(t.Context(), "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'owner')", id, owner)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return id
}

// TestTargetFoundationUpgradeRollback uses the application artifacts and a non-superuser migrator.
func (s *migrationSuite) TestTargetFoundationUpgradeRollback() {
	t := s.T()
	f := s.foundation()
	owner := f.profile(t)
	f.workspace(t, owner, "personal")
	run(t, f.dir, f.dsn, "up")
	require.Equal(t, "2", strings.TrimSpace(run(t, f.dir, f.dsn, "version")))
	run(t, f.dir, f.dsn, "down", "1")
	var count int
	require.NoError(t, f.admin.NewRaw("SELECT count(*) FROM handdraw.profiles WHERE id=?", owner).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
	run(t, f.dir, f.dsn, "up")
	f.workspace(t, owner, "team")
	run(t, f.dir, f.dsn, "down", "2")
	require.NoError(t, f.admin.NewRaw("SELECT count(*) FROM auth.users").Scan(t.Context(), &count))
	require.Equal(t, 1, count)
	var absent bool
	require.NoError(t, f.admin.NewRaw("SELECT to_regnamespace('handdraw') IS NULL AND to_regclass('public.handdraw_schema_migrations') IS NOT NULL").Scan(t.Context(), &absent))
	require.True(t, absent)
	run(t, f.dir, f.dsn, "up")
}

// TestTargetWorkspaceConstraints rejects ownerless commits, invalid personal membership and identity mutation.
func (s *migrationSuite) TestTargetWorkspaceConstraints() {
	t := s.T()
	f := s.foundation()
	owner, other := f.profile(t), f.profile(t)
	personal := f.workspace(t, owner, "personal")
	f.workspace(t, owner, "personal") // Multiple personal workspaces are allowed.
	team := f.workspace(t, owner, "team")
	missing, err := resourceid.New("ws")
	require.NoError(t, err)
	cases := []struct {
		name, query string
		args        []any
	}{
		{"ownerless", "INSERT INTO handdraw.workspaces(id,kind,name,owner_user_id) VALUES (?,'team','Missing Owner',?)", []any{missing, owner}},
		{"personal extra member", "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'viewer')", []any{personal, other}},
		{"delete owner", "DELETE FROM handdraw.workspace_members WHERE workspace_id=? AND user_id=?", []any{team, owner}},
		{"demote owner", "UPDATE handdraw.workspace_members SET role='editor' WHERE workspace_id=? AND user_id=?", []any{team, owner}},
		{"owner reference mismatch", "UPDATE handdraw.workspaces SET owner_user_id=? WHERE id=?", []any{other, team}},
		{"second owner", "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'owner')", []any{team, other}},
		{"old role spelling", "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'reader')", []any{team, other}},
		{"member identity mutation", "UPDATE handdraw.workspace_members SET user_id=? WHERE workspace_id=?", []any{other, team}},
		{"profile identity mutation", "UPDATE handdraw.profiles SET auth_user_id=gen_random_uuid() WHERE id=?", []any{owner}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.migrator.ExecContext(t.Context(), tc.query, tc.args...)
			require.Error(t, err)
			code := "23514"
			if tc.name == "second owner" {
				code = "23505"
			}
			require.Contains(t, err.Error(), code, "constraint rejection must come from the target schema")
		})
	}
}

// TestTargetWorkspaceRLS isolates workspaces and display profiles without exposing Auth mappings or writes.
func (s *migrationSuite) TestTargetWorkspaceRLS() {
	t := s.T()
	f := s.foundation()
	owner, editor, viewer, outsider := f.profile(t), f.profile(t), f.profile(t), f.profile(t)
	team := f.workspace(t, owner, "team")
	f.workspace(t, outsider, "personal")
	_, err := f.migrator.ExecContext(t.Context(), "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'editor')", team, editor)
	require.NoError(t, err)
	_, err = f.migrator.ExecContext(t.Context(), "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'viewer')", team, viewer)
	require.NoError(t, err)
	cases := []struct {
		actor                string
		workspaces, profiles int
	}{
		{"", 0, 0},
		{"bad", 0, 0},
		{"ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv", 0, 0},
		{"usr_zzzzzzzzzzzzzzzzzzzzzzzzzzz", 0, 0},
		{owner, 1, 3},
		{editor, 1, 3},
		{viewer, 1, 3},
		{outsider, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.actor, func(t *testing.T) {
			tx, err := f.request.BeginTx(t.Context(), nil)
			require.NoError(t, err)
			defer func() { require.NoError(t, tx.Rollback()) }()
			_, err = tx.ExecContext(t.Context(), "SELECT set_config('handdraw.user_id', ?, true)", tc.actor)
			require.NoError(t, err)
			var count int
			require.NoError(t, tx.NewRaw("SELECT count(*) FROM handdraw.workspaces").Scan(t.Context(), &count))
			require.Equal(t, tc.workspaces, count)
			require.NoError(t, tx.NewRaw("SELECT count(id) FROM handdraw.profiles").Scan(t.Context(), &count))
			require.Equal(t, tc.profiles, count)
			require.NoError(t, tx.NewRaw("SELECT count(*) FROM handdraw.workspace_members").Scan(t.Context(), &count))
			require.Equal(t, tc.profiles, count)
		})
	}
	for _, query := range []string{
		"SELECT auth_user_id FROM handdraw.profiles", "SELECT id FROM auth.users",
		"UPDATE handdraw.workspaces SET name='Hijacked'", "DELETE FROM handdraw.workspace_members",
		"INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES ('bad','bad','owner')",
		"TRUNCATE handdraw.workspaces CASCADE", "CREATE TABLE handdraw.rogue(id int)",
		"SET ROLE handdraw_access_owner", "SET ROLE handdraw_identity_owner",
		"SELECT * FROM handdraw.resolve_profile('bad', gen_random_uuid(), 'bad')",
	} {
		t.Run(query, func(t *testing.T) {
			require.Error(t, rlstx.Run(t.Context(), f.request, owner, func(ctx context.Context) error {
				tx, err := rlstx.Current(ctx)
				if err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, query)
				return err
			}))
		})
	}
	var before, after int64
	require.NoError(t, f.migrator.NewRaw("SELECT access_revision FROM handdraw.workspaces WHERE id=?", team).Scan(t.Context(), &before))
	_, err = f.migrator.ExecContext(t.Context(), "DELETE FROM handdraw.workspace_members WHERE workspace_id=? AND user_id=?", team, editor)
	require.NoError(t, err)
	require.NoError(t, f.migrator.NewRaw("SELECT access_revision FROM handdraw.workspaces WHERE id=?", team).Scan(t.Context(), &after))
	require.Equal(t, before+1, after)
	require.NoError(t, rlstx.Run(t.Context(), f.request, editor, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		var count int
		err = tx.NewRaw("SELECT count(*) FROM handdraw.workspaces").Scan(ctx, &count)
		require.Zero(t, count)
		return err
	}))
}

// TestTargetConcurrentMembershipAndKindChange serializes parent checks before allowing a personal workspace.
func (s *migrationSuite) TestTargetConcurrentMembershipAndKindChange() {
	t := s.T()
	f := s.foundation()
	owner, viewer := f.profile(t), f.profile(t)
	team := f.workspace(t, owner, "team")
	adding, err := f.migrator.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = adding.Rollback() }()
	_, err = adding.ExecContext(t.Context(), "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'viewer')", team, viewer)
	require.NoError(t, err)
	changing, err := f.migrator.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = changing.Rollback() }()
	_, err = changing.ExecContext(t.Context(), "SET LOCAL statement_timeout = '10s'")
	require.NoError(t, err)
	var pid int
	require.NoError(t, changing.NewRaw("SELECT pg_backend_pid()").Scan(t.Context(), &pid))
	finished := make(chan error, 1)
	go func() {
		_, changeErr := changing.ExecContext(t.Context(), "UPDATE handdraw.workspaces SET kind='personal' WHERE id=?", team)
		if changeErr == nil {
			changeErr = changing.Commit()
		}
		finished <- changeErr
	}()
	require.Eventually(t, func() bool {
		var blocked bool
		queryErr := f.admin.NewRaw("SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=? AND NOT granted)", pid).Scan(t.Context(), &blocked)
		return queryErr == nil && blocked
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, adding.Commit())
	select {
	case err := <-finished:
		require.Error(t, err)
		require.Contains(t, err.Error(), "23514")
	case <-time.After(10 * time.Second):
		t.Fatal("workspace change did not finish after membership committed")
	}
	var kind string
	require.NoError(t, f.migrator.NewRaw("SELECT kind FROM handdraw.workspaces WHERE id=?", team).Scan(t.Context(), &kind))
	require.Equal(t, "team", kind)
}

// TestTargetSecurityCatalog checks the persisted role and definer settings, including PUBLIC defaults.
func (s *migrationSuite) TestTargetSecurityCatalog() {
	t := s.T()
	f := s.foundation()
	var count int
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_roles WHERE rolname IN
 ('handdraw_request','handdraw_identity_resolver','handdraw_identity_owner','handdraw_access_owner')
 AND NOT rolcanlogin AND NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb AND NOT rolcreaterole`).Scan(t.Context(), &count))
	require.Equal(t, 4, count)
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='handdraw' AND c.relkind='r' AND c.relrowsecurity AND c.relforcerowsecurity`).Scan(t.Context(), &count))
	require.Equal(t, 3, count)
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 CROSS JOIN LATERAL aclexplode(coalesce(p.proacl, acldefault('f',p.proowner))) a
 WHERE n.nspname='handdraw' AND a.grantee=0 AND a.privilege_type='EXECUTE'`).Scan(t.Context(), &count))
	require.Zero(t, count)
	require.NoError(t, f.admin.NewRaw(`SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='handdraw' AND p.prosecdef
 AND r.rolname IN ('handdraw_identity_owner','handdraw_access_owner')
 AND p.proconfig @> ARRAY['search_path=""']::text[]`).Scan(t.Context(), &count))
	require.Equal(t, 5, count)
	for _, value := range []string{"usr_000000000000000000000000000", "usr_zzzzzzzzzzzzzzzzzzzzzzzzzzz", "usr_0ujtsYcgvSTl8PAuAdqWYSMnLOv ", "ws_0ujtsYcgvSTl8PAuAdqWYSMnLOv"} {
		var valid bool
		require.NoError(t, f.migrator.NewRaw("SELECT handdraw.valid_resource_id(?, 'usr')", value).Scan(t.Context(), &valid))
		require.False(t, valid, value)
	}
}
