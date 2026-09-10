package bunx

import (
	"context"
	"errors"

	"github.com/uptrace/bun"
)

// ErrSchemaUnavailable indicates an absent, dirty, or incompatible deployment schema.
var ErrSchemaUnavailable = errors.New("database schema is unavailable or incompatible")

// CheckSchema checks the installed version through a narrow database function.
func CheckSchema(ctx context.Context, db *bun.DB, expected int64) error {
	if db == nil || expected < 1 {
		return ErrSchemaUnavailable
	}

	var compatible bool
	if err := db.NewRaw("SELECT handdraw.schema_compatible(?)", expected).Scan(ctx, &compatible); err != nil || !compatible {
		return ErrSchemaUnavailable
	}

	return nil
}
