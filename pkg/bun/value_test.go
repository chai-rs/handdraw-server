package bunx_test

import (
	"database/sql"
	"testing"

	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNullStringTreatsEmptyAsNull(t *testing.T) {
	t.Parallel()

	assert.Equal(t, sql.NullString{}, bunx.NullString(""))
}

func TestNullStringPreservesNonEmptyValue(t *testing.T) {
	t.Parallel()

	assert.Equal(t, sql.NullString{String: "value", Valid: true}, bunx.NullString("value"))
}

func TestNullStringPtrDistinguishesNilFromEmpty(t *testing.T) {
	t.Parallel()

	empty := ""

	assert.Equal(t, sql.NullString{}, bunx.NullStringPtr(nil))
	assert.Equal(t, sql.NullString{String: "", Valid: true}, bunx.NullStringPtr(&empty))
}

func TestStringValueReturnsEmptyForNull(t *testing.T) {
	t.Parallel()

	assert.Empty(t, bunx.StringValue(sql.NullString{}))
	assert.Equal(t, "value", bunx.StringValue(sql.NullString{String: "value", Valid: true}))
}

func TestStringPtrDistinguishesNullFromValidEmpty(t *testing.T) {
	t.Parallel()

	assert.Nil(t, bunx.StringPtr(sql.NullString{}))

	value := bunx.StringPtr(sql.NullString{String: "", Valid: true})
	require.NotNil(t, value)
	assert.Empty(t, *value)
}
