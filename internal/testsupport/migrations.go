//go:build integration || migrationtest

// Package testsupport applies deployment artifacts only to caller-owned disposable databases.
package testsupport

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Migrate runs the production migration wrapper without inheriting application database credentials.
func Migrate(t *testing.T, dsn string, args ...string) {
	t.Helper()

	_, source, _, ok := runtime.Caller(0)
	require.True(t, ok)

	script := filepath.Join(filepath.Dir(source), "../../script/migrate.sh")
	u, err := url.Parse(dsn)
	require.NoError(t, err)

	query := u.Query()
	query.Set("search_path", "public")
	query.Set("x-migrations-table", "handdraw_schema_migrations")
	u.RawQuery = query.Encode()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 45*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, "sh", append([]string{script}, args...)...)

	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "DATABASE_URL" && key != "MIGRATION_DATABASE_URL" {
			command.Env = append(command.Env, entry)
		}
	}

	command.Env = append(command.Env, "MIGRATION_DATABASE_URL="+u.String())
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", strings.ReplaceAll(string(output), u.String(), "<disposable-database>"))
}
