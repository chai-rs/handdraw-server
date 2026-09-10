package model

import (
	"math"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

// ExpectedRevision is the caller's precondition for a metadata mutation.
type ExpectedRevision int64

// Validate accepts positive revisions that can advance without overflowing PostgreSQL bigint.
func (r ExpectedRevision) Validate() error {
	if err := valx.Var(int64(r), valx.Required, valx.Min(int64(1)), valx.Max(int64(math.MaxInt64-1))); err != nil {
		return ErrInvalidRevision
	}

	return nil
}
