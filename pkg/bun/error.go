package bunx

import (
	"errors"

	"github.com/uptrace/bun/driver/pgdriver"
)

// ConstraintViolation names the integrity constraint a statement broke. It is
// Postgres's SQLSTATE class 23 given names, so that an adapter can branch on
// which constraint failed rather than on a five-character string, and so that
// the branch it writes is checked by the compiler.
//
// pgdriver already answers "was this class 23 at all" with
// pgdriver.Error.IntegrityViolation. This answers which one, which is the
// difference between a 409 and a 422.
//
// https://www.postgresql.org/docs/current/errcodes-appendix.html
type ConstraintViolation string

// The constraint violations Postgres reports.
//
// NoViolation is the zero value and covers three cases a caller asking "which
// constraint failed" cannot tell apart and does not need to: a nil error, an
// error that did not come from Postgres, and a Postgres error that broke no
// constraint. Use SQLState when the distinction matters.
//
// IntegrityViolation is the class's own generic code rather than a category the
// others fall under: Postgres reports it when it has nothing more specific to
// say, so a switch that handles the specific violations still has to handle it.
const (
	NoViolation         ConstraintViolation = ""
	IntegrityViolation  ConstraintViolation = "integrity"
	RestrictViolation   ConstraintViolation = "restrict"
	NotNullViolation    ConstraintViolation = "not_null"
	ForeignKeyViolation ConstraintViolation = "foreign_key"
	UniqueViolation     ConstraintViolation = "unique"
	CheckViolation      ConstraintViolation = "check"
	ExclusionViolation  ConstraintViolation = "exclusion"
)

// SQLSTATE codes for the constraint violations above, kept beside the mapping
// that reads them so the two cannot drift.
const (
	sqlStateIntegrityViolation  = "23000"
	sqlStateRestrictViolation   = "23001"
	sqlStateNotNullViolation    = "23502"
	sqlStateForeignKeyViolation = "23503"
	sqlStateUniqueViolation     = "23505"
	sqlStateCheckViolation      = "23514"
	sqlStateExclusionViolation  = "23P01"
)

// SQLState returns the SQLSTATE Postgres reported, or the empty string when err
// did not come from Postgres at all. It unwraps, so an error already wrapped by
// the error package still answers.
func SQLState(err error) string {
	var pgErr pgdriver.Error

	if !errors.As(err, &pgErr) {
		return ""
	}

	return pgErr.Field('C')
}

// Violation reports which integrity constraint err broke, or NoViolation when it
// broke none. A nil error is NoViolation, so a caller may pass the error through
// without checking it first.
func Violation(err error) ConstraintViolation {
	return ViolationOf(SQLState(err))
}

// ViolationOf maps a SQLSTATE to the constraint it names. It is separate from
// Violation because a pgdriver.Error cannot be constructed outside its own
// package, so this is the part of the mapping that can be tested without a
// server in front of it.
func ViolationOf(sqlState string) ConstraintViolation {
	switch sqlState {
	case sqlStateIntegrityViolation:
		return IntegrityViolation
	case sqlStateRestrictViolation:
		return RestrictViolation
	case sqlStateNotNullViolation:
		return NotNullViolation
	case sqlStateForeignKeyViolation:
		return ForeignKeyViolation
	case sqlStateUniqueViolation:
		return UniqueViolation
	case sqlStateCheckViolation:
		return CheckViolation
	case sqlStateExclusionViolation:
		return ExclusionViolation
	default:
		return NoViolation
	}
}
