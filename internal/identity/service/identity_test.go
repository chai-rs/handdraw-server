package service_test

import (
	"strings"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/internal/identity/model"
	"github.com/chai-rs/handdraw-server/internal/identity/model/mocks"
	"github.com/chai-rs/handdraw-server/internal/identity/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestAuthenticateResolvesOnlyVerifiedSubject(t *testing.T) {
	verified := model.AuthIdentity{Subject: model.AuthSubject(uuid.NewString()), Email: "current@example.com", ExpiresAt: time.Now().Add(time.Hour)}
	stored, err := model.NewProfile(model.NewProfileParams{AuthUserID: verified.Subject, DisplayName: "Existing name"})
	require.NoError(t, err)
	verifier, profiles := mocks.NewMockVerifier(t), mocks.NewMockProfileRepository(t)
	token := model.AccessToken("opaque.token.value")
	verifier.EXPECT().Verify(t.Context(), token).Return(verified, nil).Once()
	profiles.EXPECT().Resolve(t.Context(), mock.MatchedBy(func(candidate model.Profile) bool {
		return candidate.AuthUserID() == string(verified.Subject) && candidate.DisplayName() == model.DefaultDisplayName && strings.HasPrefix(candidate.ID(), model.ProfileIDPrefix+"_")
	})).Return(stored, nil).Once()
	principal, err := service.New(verifier, profiles).Authenticate(t.Context(), token)
	require.NoError(t, err)
	require.Equal(t, stored.ID(), principal.Profile.ID())
	require.Equal(t, "Existing name", principal.Profile.DisplayName())
	require.Equal(t, "current@example.com", principal.Identity.Email)
}

func TestAuthenticateStopsBeforePersistence(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		token                model.AccessToken
		identity             model.AuthIdentity
		verifyErr, errorWant error
		callsProvider        bool
	}{
		{name: "missing token", errorWant: model.ErrUnauthenticated},
		{name: "provider rejects", token: "opaque", verifyErr: model.ErrUnauthenticated, errorWant: model.ErrUnauthenticated, callsProvider: true},
		{name: "provider offline", token: "opaque", verifyErr: model.ErrUnavailable, errorWant: model.ErrUnavailable, callsProvider: true},
		{name: "expired verified result", token: "opaque", identity: model.AuthIdentity{Subject: model.AuthSubject(uuid.NewString()), ExpiresAt: time.Now().Add(-time.Hour)}, errorWant: model.ErrUnauthenticated, callsProvider: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier, profiles := mocks.NewMockVerifier(t), mocks.NewMockProfileRepository(t)
			if tc.callsProvider {
				verifier.EXPECT().Verify(t.Context(), tc.token).Return(tc.identity, tc.verifyErr).Once()
			}
			_, err := service.New(verifier, profiles).Authenticate(t.Context(), tc.token)
			require.ErrorIs(t, err, tc.errorWant)
		})
	}
}

func TestAuthenticateRejectsResolverFailures(t *testing.T) {
	verified := model.AuthIdentity{Subject: model.AuthSubject(uuid.NewString()), ExpiresAt: time.Now().Add(time.Hour)}
	foreign, err := model.NewProfile(model.NewProfileParams{AuthUserID: model.AuthSubject(uuid.NewString()), DisplayName: "Other"})
	require.NoError(t, err)
	for _, tc := range []struct {
		name          string
		profile       model.Profile
		repoErr, want error
	}{
		{name: "deleted profile", repoErr: model.ErrUnauthenticated, want: model.ErrUnauthenticated},
		{name: "database unavailable", repoErr: model.ErrUnavailable, want: model.ErrUnavailable},
		{name: "wrong subject mapping", profile: foreign, want: model.ErrUnauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier, profiles := mocks.NewMockVerifier(t), mocks.NewMockProfileRepository(t)
			verifier.EXPECT().Verify(t.Context(), model.AccessToken("opaque")).Return(verified, nil).Once()
			profiles.EXPECT().Resolve(t.Context(), mock.MatchedBy(func(candidate model.Profile) bool {
				return candidate.AuthUserID() == string(verified.Subject) && candidate.DisplayName() == model.DefaultDisplayName
			})).Return(tc.profile, tc.repoErr).Once()
			_, err := service.New(verifier, profiles).Authenticate(t.Context(), "opaque")
			require.ErrorIs(t, err, tc.want)
		})
	}
}
