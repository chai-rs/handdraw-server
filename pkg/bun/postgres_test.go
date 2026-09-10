package bunx_test

import (
	"testing"

	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/stretchr/testify/require"
)

func TestPostgresConfigurationErrorsDoNotPanicOrExposeCredentials(t *testing.T) {
	for _, dsn := range []string{"not-a-url", "postgres://user:secret@remote.example/db?sslmode=disable", "postgres://user:secret@localhost/db?read_timeout=invalid"} {
		t.Run(dsn, func(t *testing.T) {
			require.NotPanics(t, func() {
				db, err := (bunx.PGConfig{URL: dsn}).New(t.Context())
				require.Error(t, err)
				require.Nil(t, db)
				require.NotContains(t, err.Error(), "secret")
			})
		})
	}
}
