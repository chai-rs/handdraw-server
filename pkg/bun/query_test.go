package bunx

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
)

type widget struct {
	bun.BaseModel `bun:"table:widgets,alias:widget"`

	Name   string `bun:"name"`
	Status string `bun:"status"`
}

// newRenderDB returns a DB that renders SQL and never connects. sql.OpenDB is
// lazy and SelectQuery.String only asks the dialect to generate the statement,
// so no Postgres is contacted and none is needed.
func newRenderDB(t *testing.T) *bun.DB {
	t.Helper()

	sqldb := sql.OpenDB(pgdriver.NewConnector())
	t.Cleanup(func() { _ = sqldb.Close() })

	return bun.NewDB(sqldb, pgdialect.New())
}

func TestWhereAnyOfRendersOneCaseForEachSizeOfFilter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		values      []string
		wantSQL     string
		wantNotSQL  string
		wantNoWhere bool
	}{
		{
			name:        "no values is not a filter at all",
			values:      nil,
			wantNoWhere: true,
		},
		{
			name:    "one value is an equality, not a single-element IN",
			values:  []string{"open"},
			wantSQL: `"widget"."status" = 'open'`,
			// An IN here would be valid SQL but a different plan, and it hides
			// the far worse case below.
			wantNotSQL: "IN",
		},
		{
			name:    "several values are an IN",
			values:  []string{"open", "closed"},
			wantSQL: `"widget"."status" IN ('open', 'closed')`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			is := assert.New(t)

			query := newRenderDB(t).NewSelect().Model((*widget)(nil))
			WhereAnyOf(query.QueryBuilder(), "status", c.values)

			got := query.String()

			if c.wantNoWhere {
				is.NotContains(got, "WHERE", "an empty filter must not narrow the read")

				return
			}

			is.Contains(got, c.wantSQL)

			if c.wantNotSQL != "" {
				is.NotContains(got, c.wantNotSQL)
			}
		})
	}
}

// The column has to be quoted rather than pasted in, or a column name that
// collides with a keyword — or that came from a caller — reaches the server as
// bare SQL.
func TestWhereAnyOfQuotesTheColumn(t *testing.T) {
	t.Parallel()

	is := assert.New(t)

	query := newRenderDB(t).NewSelect().Model((*widget)(nil))
	WhereAnyOf(query.QueryBuilder(), "name", []string{"a"})

	is.Contains(query.String(), `"widget"."name"`)
}

// Every condition has to land on the query the builder was taken from, since
// callers discard the returned builder. If it did not, a filter would silently
// render as no condition at all and the read would return the whole table.
func TestWhereAnyOfAccumulatesOntoTheQueryItWasTakenFrom(t *testing.T) {
	t.Parallel()

	is := assert.New(t)
	require := require.New(t)

	query := newRenderDB(t).NewSelect().Model((*widget)(nil))

	qb := query.QueryBuilder()
	WhereAnyOf(qb, "name", []string{"a"})
	WhereAnyOf(qb, "status", []string{"open", "closed"})

	got := query.String()
	require.Contains(got, "WHERE")

	is.Contains(got, `"widget"."name" = 'a'`)
	is.Contains(got, `"widget"."status" IN ('open', 'closed')`)
}

// The two bounds render as inclusive comparisons on the column they were given,
// and a nil bound renders as nothing at all. Getting the direction backwards
// would still be valid SQL, so the operator is asserted rather than assumed.
func TestWhereAtOrAfterAndBeforeRenderInclusiveBounds(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name        string
		apply       func(qb bun.QueryBuilder)
		wantSQL     string
		wantNotSQL  string
		wantNoWhere bool
	}{
		{
			name:        "no lower bound is not a filter at all",
			apply:       func(qb bun.QueryBuilder) { WhereAtOrAfter(qb, "created_at", nil) },
			wantNoWhere: true,
		},
		{
			name:        "no upper bound is not a filter at all",
			apply:       func(qb bun.QueryBuilder) { WhereAtOrBefore(qb, "created_at", nil) },
			wantNoWhere: true,
		},
		{
			name:  "a lower bound is inclusive",
			apply: func(qb bun.QueryBuilder) { WhereAtOrAfter(qb, "created_at", &at) },
			// >= rather than >, and the exclusive form must not appear on its own.
			wantSQL:    `"widget"."created_at" >= '2026-08-05 00:00:00+00:00'`,
			wantNotSQL: `"widget"."created_at" > '`,
		},
		{
			name:       "an upper bound is inclusive",
			apply:      func(qb bun.QueryBuilder) { WhereAtOrBefore(qb, "created_at", &at) },
			wantSQL:    `"widget"."created_at" <= '2026-08-05 00:00:00+00:00'`,
			wantNotSQL: `"widget"."created_at" < '`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			is := assert.New(t)

			query := newRenderDB(t).NewSelect().Model((*widget)(nil))
			c.apply(query.QueryBuilder())

			got := query.String()

			if c.wantNoWhere {
				is.NotContains(got, "WHERE", "an absent bound must not narrow the read")

				return
			}

			is.Contains(got, c.wantSQL)

			if c.wantNotSQL != "" {
				is.NotContains(got, c.wantNotSQL)
			}
		})
	}
}

// A zero time is a bound like any other, which is the whole reason the parameter
// is a pointer. Read through IsZero it would silently become "no bound".
func TestWhereAtOrBeforeTreatsTheZeroTimeAsARealBound(t *testing.T) {
	t.Parallel()

	is := assert.New(t)

	var zero time.Time

	query := newRenderDB(t).NewSelect().Model((*widget)(nil))
	WhereAtOrBefore(query.QueryBuilder(), "created_at", &zero)

	is.Contains(query.String(), `"widget"."created_at" <=`)
}

// The two ends close a range on one column, and two columns narrow separately.
// Both land on the query the builder came from, as WhereAnyOf's cases require.
func TestWhereAtOrAfterAndBeforeComposeOntoOneQuery(t *testing.T) {
	t.Parallel()

	is := assert.New(t)
	require := require.New(t)

	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)

	query := newRenderDB(t).NewSelect().Model((*widget)(nil))

	qb := query.QueryBuilder()
	WhereAtOrAfter(qb, "created_at", &from)
	WhereAtOrBefore(qb, "created_at", &to)
	WhereAtOrAfter(qb, "updated_at", &to)
	WhereAnyOf(qb, "status", []string{"open"})

	got := query.String()
	require.Contains(got, "WHERE")

	is.Contains(got, `"widget"."created_at" >= '2026-08-01 00:00:00+00:00'`)
	is.Contains(got, `"widget"."created_at" <= '2026-08-09 00:00:00+00:00'`)
	is.Contains(got, `"widget"."updated_at" >= '2026-08-09 00:00:00+00:00'`)
	is.Contains(got, `"widget"."status" = 'open'`)
}
