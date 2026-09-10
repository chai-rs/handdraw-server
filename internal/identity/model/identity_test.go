package model_test

import (
	"strings"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/internal/identity/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestInvalidCredentials(t *testing.T) {
	for _, token := range []string{"", "a b", "a\nb", strings.Repeat("a", model.MaxAccessTokenBytes+1), "a\x00", "\xff"} {
		require.ErrorIs(t, model.AccessToken(token).Validate(), model.ErrUnauthenticated)
	}
	valid := "550e8400-e29b-41d4-a716-446655440000"
	require.NoError(t, model.AuthSubject(valid).Validate())
	for _, subject := range []string{"", uuid.Nil.String(), strings.ToUpper(valid), strings.ReplaceAll(valid, "-", ""), "usr_abc"} {
		require.ErrorIs(t, model.AuthSubject(subject).Validate(), model.ErrUnauthenticated)
	}
}

func TestProfileNormalizesNameAndOwnsPrefix(t *testing.T) {
	params := model.NewProfileParams{AuthUserID: model.AuthSubject(uuid.NewString()), DisplayName: "  Developer ไทย  "}
	profile, err := model.NewProfile(params)
	require.NoError(t, err)
	require.Equal(t, "  Developer ไทย  ", params.DisplayName)
	require.Equal(t, "Developer ไทย", profile.DisplayName())
	require.NoError(t, resourceid.Validate(profile.ID(), model.ProfileIDPrefix))
	require.True(t, profile.CreatedAt().IsZero())
	now := time.Now()
	hydrated, err := model.RehydrateProfile(model.RehydrateProfileParams{ID: profile.ID(), AuthUserID: params.AuthUserID, DisplayName: profile.DisplayName(), CreatedAt: now, UpdatedAt: now})
	require.NoError(t, err)
	require.Equal(t, profile.ID(), hydrated.ID())
	require.Equal(t, now, hydrated.CreatedAt())
	for _, name := range []string{"", "\x00", "\xff", strings.Repeat("ก", model.MaxDisplayNameRunes+1)} {
		params.DisplayName = name
		_, err := model.NewProfile(params)
		require.ErrorIs(t, err, model.ErrInvalidProfile)
	}
}
