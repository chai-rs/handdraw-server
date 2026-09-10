package model_test

import (
	"strings"
	"testing"

	"github.com/chai-rs/handdraw-server/internal/idempotency/model"
	"github.com/stretchr/testify/require"
)

func TestRequestNormalizesUUIDBeforeLocking(t *testing.T) {
	key := "52ef1b34-0161-49b2-8107-5adad101fb4c"
	a, err := model.New("workspace.create", key, "", map[string]string{"name": "Architecture"})
	require.NoError(t, err)
	b, err := model.New("workspace.create", strings.ToUpper(key), "", map[string]string{"name": "Architecture"})
	require.NoError(t, err)
	require.Equal(t, a, b)
}

func TestRequestRejectsInvalidKeys(t *testing.T) {
	for _, key := range []string{"", "00000000-0000-0000-0000-000000000000", "not-a-uuid", "52ef1b34016149b281075adad101fb4c"} {
		t.Run(key, func(t *testing.T) {
			_, err := model.New("workspace.create", key, "", map[string]string{"name": "Architecture"})
			require.ErrorIs(t, err, model.ErrInvalid)
		})
	}
}
