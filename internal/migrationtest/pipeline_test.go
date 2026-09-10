//go:build migrationtest

// Package migrationtest_test verifies the migration wrapper against disposable PostgreSQL databases.
package migrationtest_test

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/uptrace/bun"
)

//go:embed testdata/*.sql
var fixtures embed.FS

type toolVersions struct {
	Migrate       string `json:"golang_migrate"`
	PostgresImage string `json:"postgres_image"`
}

type migrationSuite struct {
	suite.Suite
	admin   *bun.DB
	baseURL *url.URL
	serial  int
}

// TestMigrationSuite applies handwritten SQL through the production shell wrapper and real migrate CLI.
func TestMigrationSuite(t *testing.T) { suite.Run(t, new(migrationSuite)) }

// SetupSuite verifies the CLI version and owns all test database credentials and cleanup.
func (s *migrationSuite) SetupSuite() {
	t := s.T()
	data, err := os.ReadFile("../../tools/migration-versions.json")
	require.NoError(t, err)
	var versions toolVersions
	require.NoError(t, json.Unmarshal(data, &versions))
	require.NotEmpty(t, versions.Migrate)
	require.NotEmpty(t, versions.PostgresImage)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "migrate", "-version").CombinedOutput()
	require.NoError(t, err)
	require.Equal(t, versions.Migrate, strings.TrimSpace(string(output)))
	db, err := postgres.Run(ctx, versions.PostgresImage, postgres.WithDatabase("migration_admin"), postgres.WithUsername("handdraw_test_admin"), postgres.WithPassword("local_test_only"), postgres.BasicWaitStrategies(), testcontainers.WithHostConfigModifier(func(config *container.HostConfig) {
		for port, bindings := range config.PortBindings {
			for i := range bindings {
				bindings[i].HostIP = netip.MustParseAddr("127.0.0.1")
			}
			config.PortBindings[port] = bindings
		}
	}))
	testcontainers.CleanupContainer(t, db)
	require.NoError(t, err)
	dsn, err := db.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	s.baseURL, err = url.Parse(dsn)
	require.NoError(t, err)
	s.admin = openDB(t, dsn)
}

func openDB(t *testing.T, dsn string) *bun.DB {
	t.Helper()
	db, err := (bunx.PGConfig{URL: dsn}).New(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func (s *migrationSuite) prepare() (string, string, string, *bun.DB) {
	t := s.T()
	s.serial++
	name := fmt.Sprintf("migration_probe_%d", s.serial)
	_, err := s.admin.ExecContext(t.Context(), "CREATE DATABASE ?", bun.Ident(name))
	require.NoError(t, err)
	u := *s.baseURL
	u.Path = "/" + name
	q := u.Query()
	q.Set("search_path", "public")
	q.Set("x-migrations-table", "handdraw_schema_migrations")
	u.RawQuery = q.Encode()
	poolURL := u
	poolQuery := poolURL.Query()
	poolQuery.Del("x-migrations-table")
	poolURL.RawQuery = poolQuery.Encode()
	db := openDB(t, poolURL.String())
	dir := wrapperDir(t)
	role := fmt.Sprintf("handdraw_probe_reader_%d", s.serial)
	entries, err := fixtures.ReadDir("testdata")
	require.NoError(t, err)
	for _, entry := range entries {
		contents, err := fixtures.ReadFile("testdata/" + entry.Name())
		require.NoError(t, err)
		write(t, filepath.Join(dir, "migrations", entry.Name()), strings.ReplaceAll(string(contents), "handdraw_probe_reader", role))
	}
	return dir, u.String(), role, db
}

func wrapperDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "script"), 0o700))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "migrations"), 0o700))
	script, err := os.ReadFile("../../script/migrate.sh")
	require.NoError(t, err)
	write(t, filepath.Join(dir, "script", "migrate.sh"), string(script))
	return dir
}

func command(t *testing.T, dir, dsn string, args ...string) (string, error) {
	t.Helper()
	// Cleanup runs after the test context is canceled; retain a bounded rollback deadline.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", append([]string{filepath.Join(dir, "script", "migrate.sh")}, args...)...)
	cmd.Dir = dir
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "DATABASE_URL" && key != "MIGRATION_DATABASE_URL" {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "MIGRATION_DATABASE_URL="+dsn)
	output, err := cmd.CombinedOutput()
	result := string(output)
	if dsn != "" {
		result = strings.ReplaceAll(result, dsn, "<disposable-database>")
	}
	return result, err
}

func run(t *testing.T, dir, dsn string, args ...string) string {
	t.Helper()
	output, err := command(t, dir, dsn, args...)
	require.NoError(t, err, "%s", output)
	return output
}

func write(t *testing.T, path, text string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))
}

// TestReplayUpgradeRollback preserves data across upgrade and keeps history/external schemas outside app DDL.
func (s *migrationSuite) TestReplayUpgradeRollback() {
	t := s.T()
	dir, dsn, _, db := s.prepare()
	_, err := db.ExecContext(t.Context(), "CREATE SCHEMA auth; CREATE TABLE auth.users(id uuid PRIMARY KEY, email text, email_confirmed_at timestamptz)")
	require.NoError(t, err)
	run(t, dir, dsn, "up", "1")
	const actor = "usr_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	_, err = db.ExecContext(t.Context(), "INSERT INTO handdraw_probe.principals(id) VALUES (?)", actor)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "INSERT INTO handdraw_probe.principals(id) VALUES ('invalid')")
	require.Error(t, err)
	_, err = db.ExecContext(t.Context(), "INSERT INTO handdraw_probe.boards(id,owner_id) VALUES ('board','missing')")
	require.Error(t, err)
	_, err = db.ExecContext(t.Context(), "INSERT INTO handdraw_probe.boards(id,owner_id) VALUES ('board',?)", actor)
	require.NoError(t, err)
	run(t, dir, dsn, "up")
	require.Equal(t, "2", strings.TrimSpace(run(t, dir, dsn, "version")))
	var name string
	require.NoError(t, db.NewRaw("SELECT name FROM handdraw_probe.boards WHERE id='board'").Scan(t.Context(), &name))
	require.Equal(t, "Untitled", name)
	run(t, dir, dsn, "up")
	run(t, dir, dsn, "down", "1")
	var exists bool
	require.NoError(t, db.NewRaw("SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='handdraw_probe' AND table_name='boards' AND column_name='name')").Scan(t.Context(), &exists))
	require.False(t, exists)
	run(t, dir, dsn, "up")
	run(t, dir, dsn, "down", "2")
	require.NoError(t, db.NewRaw("SELECT to_regclass('public.handdraw_schema_migrations') IS NOT NULL AND to_regclass('auth.users') IS NOT NULL").Scan(t.Context(), &exists))
	require.True(t, exists)
	require.NoError(t, db.NewRaw("SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='handdraw_probe')").Scan(t.Context(), &exists))
	require.False(t, exists)
	run(t, dir, dsn, "up")
	require.Equal(t, "2", strings.TrimSpace(run(t, dir, dsn, "version")))
}

// TestSecurityObjectsEnforcePermissions verifies policies, grants and triggers applied by the same runner as tables.
func (s *migrationSuite) TestSecurityObjectsEnforcePermissions() {
	t := s.T()
	dir, dsn, role, db := s.prepare()
	run(t, dir, dsn, "up")
	const actor = "usr_0ujtsYcgvSTl8PAuAdqWYSMnLOv"
	const other = "usr_0ujtsYcgvSTl8PAuAdqWYSMnLOw"
	_, err := db.ExecContext(t.Context(), "INSERT INTO handdraw_probe.principals(id) VALUES (?), (?)", actor, other)
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), "UPDATE handdraw_probe.principals SET id='usr_0ujtsYcgvSTl8PAuAdqWYSMnLOx' WHERE id=?", actor)
	require.ErrorContains(t, err, "immutable identity")
	var enabled, forced bool
	require.NoError(t, db.NewRaw("SELECT relrowsecurity,relforcerowsecurity FROM pg_class WHERE oid='handdraw_probe.principals'::regclass").Scan(t.Context(), &enabled, &forced))
	require.True(t, enabled)
	require.True(t, forced)
	for _, current := range []string{"", actor, other} {
		err := db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
			if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE ?", bun.Ident(role)); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "SELECT set_config('handdraw.user_id',?,true)", current); err != nil {
				return err
			}
			var values []string
			if err := tx.NewRaw("SELECT id FROM handdraw_probe.principals").Scan(ctx, &values); err != nil {
				return err
			}
			if current == "" {
				require.Empty(t, values)
			} else {
				require.Equal(t, []string{current}, values)
			}
			return nil
		})
		require.NoError(t, err)
	}
	err = db.RunInTx(t.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE ?", bun.Ident(role)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE handdraw_probe.principals SET id=id")
		return err
	})
	require.Error(t, err)
	run(t, dir, dsn, "down", "2")
	var exists bool
	require.NoError(t, db.NewRaw("SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=?)", role).Scan(t.Context(), &exists))
	require.False(t, exists)
}

// TestFailedMigrationRollsBackAndLeavesDirtyHistory requires explicit recovery instead of automatic force.
func (s *migrationSuite) TestFailedMigrationRollsBackAndLeavesDirtyHistory() {
	t := s.T()
	dir, dsn, _, db := s.prepare()
	run(t, dir, dsn, "up")
	write(t, filepath.Join(dir, "migrations", "000003_failure.up.sql"), "BEGIN; CREATE TABLE handdraw_probe.must_rollback(id int); SELECT 1/0; COMMIT;")
	write(t, filepath.Join(dir, "migrations", "000003_failure.down.sql"), "DROP TABLE handdraw_probe.must_rollback;")
	output, err := command(t, dir, dsn, "up")
	require.Error(t, err)
	require.Contains(t, output, "Tool error output is hidden")
	var dirty, exists bool
	require.NoError(t, db.NewRaw("SELECT dirty FROM public.handdraw_schema_migrations WHERE version=3").Scan(t.Context(), &dirty))
	require.True(t, dirty)
	require.NoError(t, db.NewRaw("SELECT to_regclass('handdraw_probe.must_rollback') IS NOT NULL").Scan(t.Context(), &exists))
	require.False(t, exists)
	_, err = command(t, dir, dsn, "up")
	require.Error(t, err)
}

// TestCreateAndArgumentValidation exercises the wrapper without a database connection.
func TestCreateAndArgumentValidation(t *testing.T) {
	dir := wrapperDir(t)
	run(t, dir, "", "create", "initial")
	run(t, dir, "", "create", "board_name")
	for _, name := range []string{"000001_initial.up.sql", "000001_initial.down.sql", "000002_board_name.up.sql", "000002_board_name.down.sql"} {
		require.FileExists(t, filepath.Join(dir, "migrations", name))
	}
	for _, args := range [][]string{{"create", "../escape"}, {"create", ""}, {"create", "unsafe;command"}, {"down"}, {"down", "0"}, {"down", "01"}, {"up", "-1"}, {"force", "0"}, {"drop"}, {"up"}} {
		_, err := command(t, dir, "", args...)
		require.Error(t, err, args)
	}
}

// TestHistorySettingsRejectAmbiguousURLs prevents an override from choosing another history table.
func TestHistorySettingsRejectAmbiguousURLs(t *testing.T) {
	dir := wrapperDir(t)
	for _, test := range []struct{ name, query, want string }{
		{"missing schema", "x-migrations-table=handdraw_schema_migrations", "search_path=public"},
		{"wrong history", "search_path=public&x-migrations-table=other", "x-migrations-table=handdraw_schema_migrations"},
		{"duplicate schema", "search_path=public&search_path=other&x-migrations-table=handdraw_schema_migrations", "must not be duplicated"},
		{"duplicate history", "search_path=public&x-migrations-table=handdraw_schema_migrations&x-migrations-table=other", "must not be duplicated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := command(t, dir, "postgres://local_test_only@127.0.0.1:1/test?"+test.query, "up")
			require.Error(t, err)
			require.Contains(t, output, test.want)
		})
	}
}
