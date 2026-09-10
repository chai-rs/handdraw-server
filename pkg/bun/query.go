package bunx

import (
	"time"

	"github.com/uptrace/bun"
)

// WhereAnyOf narrows column to values and returns the builder. The three cases
// are not interchangeable: no values is not a filter at all, one value renders
// as an equality, and several render as an IN. A caller that always emitted IN
// would turn an empty filter into `IN ()`, which Postgres rejects, and would
// make an unset filter field mean "match nothing" rather than "do not narrow on
// this".
//
// The column is passed through bun.Ident so that it is quoted rather than pasted
// into the statement, and it is qualified with ?TableAlias so the condition stays
// unambiguous once the query joins. That qualification is also the one
// precondition: the query must have been built from a model, since a builder
// taken from a modelless NewSelect() has no alias to substitute and renders
// invalid SQL.
//
// The builder wraps the query rather than replacing it, so the returned value can
// be discarded when the conditions are all that matter:
//
//	query := db.NewSelect().Model(&rows)
//	bunx.WhereAnyOf(query.QueryBuilder(), "status", statuses)
func WhereAnyOf[T any](qb bun.QueryBuilder, column string, values []T) bun.QueryBuilder {
	switch len(values) {
	case 0:
		return qb
	case 1:
		return qb.Where("?TableAlias.? = ?", bun.Ident(column), values[0])
	default:
		return qb.Where("?TableAlias.? IN (?)", bun.Ident(column), bun.List(values))
	}
}

// WhereAtOrAfter narrows column to rows at or after at, and WhereAtOrBefore to
// rows at or before it. Both bounds are inclusive, and a nil at is not a filter,
// so the two compose into a closed range, a half-open one, or no range at all
// without either needing to know what the other was given.
//
// The bound is a pointer rather than a time.Time read through IsZero. Most of
// what a filter holds can treat its zero value as "unset" because an empty slice
// has nothing to say, but a time.Time always denotes some instant, and reading
// year 1 as "no bound" would refuse to express a bound a caller could mean.
//
// It is dereferenced before it reaches bun, which would otherwise bind the
// pointer itself as a parameter and compare the column against something
// Postgres cannot read.
//
// Both share WhereAnyOf's two rules: the column goes through bun.Ident so it is
// quoted rather than pasted into the statement, and it is qualified with
// ?TableAlias so the condition stays unambiguous once the query joins — which is
// also the precondition, since a builder taken from a modelless NewSelect() has
// no alias to substitute.
func WhereAtOrAfter(qb bun.QueryBuilder, column string, at *time.Time) bun.QueryBuilder {
	if at == nil {
		return qb
	}

	return qb.Where("?TableAlias.? >= ?", bun.Ident(column), *at)
}

// WhereAtOrBefore narrows column to rows at or before at. See WhereAtOrAfter.
func WhereAtOrBefore(qb bun.QueryBuilder, column string, at *time.Time) bun.QueryBuilder {
	if at == nil {
		return qb
	}

	return qb.Where("?TableAlias.? <= ?", bun.Ident(column), *at)
}
