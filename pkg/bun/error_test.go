package bunx

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every SQLSTATE in class 23 maps to a name, and everything else maps to
// NoViolation. The codes are spelled out here rather than referenced from the
// constants the mapping uses, so that a typo in one of those constants shows up
// as a failure instead of agreeing with itself.
func TestViolationOfNamesEveryIntegrityConstraint(t *testing.T) {
	t.Parallel()

	cases := []struct {
		sqlState string
		want     ConstraintViolation
	}{
		{"23000", IntegrityViolation},
		{"23001", RestrictViolation},
		{"23502", NotNullViolation},
		{"23503", ForeignKeyViolation},
		{"23505", UniqueViolation},
		{"23514", CheckViolation},
		{"23P01", ExclusionViolation},
	}

	for _, c := range cases {
		t.Run(c.sqlState, func(t *testing.T) {
			t.Parallel()

			assert.New(t).Equal(c.want, ViolationOf(c.sqlState))
		})
	}
}

func TestViolationOfIsNoViolationForAnythingOutsideClass23(t *testing.T) {
	t.Parallel()

	// 57014 is a statement timeout, 42P01 an undefined table, 23504 a code
	// Postgres does not define. None of them broke a constraint.
	for _, sqlState := range []string{"", "57014", "42P01", "23504", "2350", "235050", "unknown"} {
		t.Run(fmt.Sprintf("%q", sqlState), func(t *testing.T) {
			t.Parallel()

			assert.New(t).Equal(NoViolation, ViolationOf(sqlState))
		})
	}
}

// A caller passes whatever the driver handed back, which is often not a Postgres
// error and sometimes nothing at all. Neither may panic, and neither may be
// mistaken for a constraint failure.
func TestSQLStateAndViolationTolerateErrorsThatAreNotFromPostgres(t *testing.T) {
	t.Parallel()

	is := assert.New(t)

	for _, err := range []error{
		nil,
		errors.New("plain"),
		fmt.Errorf("wrapped: %w", errors.New("plain")),
	} {
		is.Empty(SQLState(err))
		is.Equal(NoViolation, Violation(err))
	}
}

// The named violations must stay distinct: two mapping to the same string would
// make a switch on them silently unreachable in one arm.
func TestTheNamedViolationsAreDistinct(t *testing.T) {
	t.Parallel()

	all := []ConstraintViolation{
		NoViolation,
		IntegrityViolation,
		RestrictViolation,
		NotNullViolation,
		ForeignKeyViolation,
		UniqueViolation,
		CheckViolation,
		ExclusionViolation,
	}

	seen := make(map[ConstraintViolation]bool, len(all))
	for _, v := range all {
		seen[v] = true
	}

	assert.New(t).Len(seen, len(all))
}
