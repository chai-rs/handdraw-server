package supabase_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chai-rs/handdraw-server/internal/identity/infra/supabase"
	"github.com/chai-rs/handdraw-server/internal/identity/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var signingKey = []byte("local-test-signing-key-only")

func claimsFor(issuer, subject string) jwt.MapClaims {
	return jwt.MapClaims{"iss": issuer + "/auth/v1", "aud": "authenticated", "sub": subject, "role": "authenticated", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Add(-time.Minute).Unix()}
}

func signed(t *testing.T, claims jwt.MapClaims, key []byte) model.AccessToken {
	t.Helper()
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	require.NoError(t, err)
	return model.AccessToken(raw)
}

func TestVerifyUsesAuthAuthorityAndCurrentEmail(t *testing.T) {
	subject := uuid.NewString()
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/auth/v1/user" || r.Method != http.MethodGet || r.Header.Get("apikey") != "test-public-key" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, err := jwt.Parse(raw, func(*jwt.Token) (any, error) { return signingKey, nil }, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": subject, "email": "current@example.com", "email_confirmed_at": time.Now().UTC()})
	}))
	defer provider.Close()
	verifier, err := supabase.New(supabase.Config{ProjectURL: provider.URL, PublishableKey: "test-public-key"})
	require.NoError(t, err)
	claims := claimsFor(provider.URL, subject)
	claims["email"] = "old@example.com"
	verified, err := verifier.Verify(t.Context(), signed(t, claims, signingKey))
	require.NoError(t, err)
	require.Equal(t, model.AuthSubject(subject), verified.Subject)
	require.Equal(t, "current@example.com", verified.Email)
	require.True(t, verified.EmailVerified)
	_, err = verifier.Verify(t.Context(), signed(t, claims, []byte("forged-signature")))
	require.ErrorIs(t, err, model.ErrUnauthenticated)
	require.Equal(t, int32(2), calls.Load())
}

func TestVerifyRejectsInvalidClaimsBeforeProvider(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer provider.Close()
	verifier, err := supabase.New(supabase.Config{ProjectURL: provider.URL, PublishableKey: "test-key"})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, field string
		value       any
	}{
		{"issuer", "iss", "https://other.example/auth/v1"},
		{"audience", "aud", "service_role"},
		{"expired", "exp", time.Now().Add(-time.Hour).Unix()},
		{"missing expiry", "exp", nil},
		{"future issued", "iat", time.Now().Add(time.Hour).Unix()},
		{"not yet valid", "nbf", time.Now().Add(time.Hour).Unix()},
		{"privileged role", "role", "service_role"},
		{"anonymous", "is_anonymous", true},
		{"invalid subject", "sub", uuid.Nil.String()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := claimsFor(provider.URL, uuid.NewString())
			if tc.value == nil {
				delete(claims, tc.field)
			} else {
				claims[tc.field] = tc.value
			}
			_, err := verifier.Verify(t.Context(), signed(t, claims, signingKey))
			require.ErrorIs(t, err, model.ErrUnauthenticated)
		})
	}
	require.Zero(t, calls.Load())
}

func TestVerifyProviderFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"rejected", 401, "", model.ErrUnauthenticated},
		{"forbidden", 403, "", model.ErrUnauthenticated},
		{"rate limited", 429, "", model.ErrUnavailable},
		{"offline", 500, "", model.ErrUnavailable},
		{"redirect", 302, "", model.ErrUnavailable},
		{"malformed", 200, "not json", model.ErrUnavailable},
		{"oversized", 200, strings.Repeat("a", 65537), model.ErrUnavailable},
		{"wrong identity", 200, `{"id":"` + uuid.NewString() + `"}`, model.ErrUnauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var redirected atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected.Add(1); w.WriteHeader(200) }))
			defer target.Close()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer provider.Close()
			verifier, err := supabase.New(supabase.Config{ProjectURL: provider.URL, PublishableKey: "test-key"})
			require.NoError(t, err)
			_, err = verifier.Verify(t.Context(), signed(t, claimsFor(provider.URL, uuid.NewString()), signingKey))
			require.ErrorIs(t, err, tc.want)
			require.Zero(t, redirected.Load())
		})
	}
}

func TestVerifyTimeout(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer provider.Close()
	verifier, err := supabase.New(supabase.Config{ProjectURL: provider.URL, PublishableKey: "test-key", Timeout: 20 * time.Millisecond})
	require.NoError(t, err)
	_, err = verifier.Verify(t.Context(), signed(t, claimsFor(provider.URL, uuid.NewString()), signingKey))
	require.ErrorIs(t, err, model.ErrUnavailable)
}

func TestConfigRejectsUntrustedDestinations(t *testing.T) {
	for _, project := range []string{"http://remote.example", "https://user:secret@example.com", "https://example.com/wrong", "https://example.com?redirect=x", "https://example.com#fragment", ""} {
		_, err := supabase.New(supabase.Config{ProjectURL: project, PublishableKey: "test-key"})
		require.Error(t, err)
	}
}
