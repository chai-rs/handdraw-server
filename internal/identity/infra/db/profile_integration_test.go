//go:build integration

package db_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	identityapi "github.com/chai-rs/handdraw-server/internal/identity/inbound/api"
	"github.com/chai-rs/handdraw-server/internal/identity/inbound/api/dto"
	identitydb "github.com/chai-rs/handdraw-server/internal/identity/infra/db"
	"github.com/chai-rs/handdraw-server/internal/identity/infra/supabase"
	"github.com/chai-rs/handdraw-server/internal/identity/model"
	"github.com/chai-rs/handdraw-server/internal/identity/service"
	"github.com/chai-rs/handdraw-server/internal/testsupport"
	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/moby/moby/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/uptrace/bun"
)

//go:embed testdata/schema.sql
var fixture string

type profileSuite struct {
	suite.Suite
	admin    *bun.DB
	resolver *bun.DB
}

func TestProfileSuite(t *testing.T) { suite.Run(t, new(profileSuite)) }

// SetupSuite isolates authentication bootstrap from every application database.
func (s *profileSuite) SetupSuite() {
	t := s.T()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	database, err := postgres.Run(ctx, "postgres:17", postgres.WithDatabase("handdraw_identity_test"), postgres.WithUsername("handdraw_test_admin"), postgres.WithPassword("local_test_only"), postgres.BasicWaitStrategies(), testcontainers.WithHostConfigModifier(func(config *container.HostConfig) {
		for port, bindings := range config.PortBindings {
			for i := range bindings {
				bindings[i].HostIP = netip.MustParseAddr("127.0.0.1")
			}
			config.PortBindings[port] = bindings
		}
	}))
	testcontainers.CleanupContainer(t, database)
	require.NoError(t, err)
	dsn, err := database.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	s.admin = openDB(t, dsn)
	_, err = s.admin.ExecContext(ctx, fixture)
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	testsupport.Migrate(t, dsn, "up")
	_, err = s.admin.ExecContext(ctx, "CREATE ROLE identity_test_login LOGIN PASSWORD 'local_test_only' IN ROLE handdraw_identity_resolver")
	require.NoError(t, err)
	u.User = url.UserPassword("identity_test_login", "local_test_only")
	s.resolver = openDB(t, u.String())
}

// SetupTest prevents identity state leaking between cases.
func (s *profileSuite) SetupTest() {
	_, err := s.admin.ExecContext(s.T().Context(), "TRUNCATE handdraw.profiles, auth.users CASCADE")
	s.Require().NoError(err)
}

func openDB(t *testing.T, dsn string) *bun.DB {
	t.Helper()
	db, err := (bunx.PGConfig{URL: dsn, MaxOpenConns: 8}).New(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func (s *profileSuite) subject() model.AuthSubject {
	subject := model.AuthSubject(uuid.NewString())
	_, err := s.admin.ExecContext(s.T().Context(), "INSERT INTO auth.users(id) VALUES (?::uuid)", subject)
	s.Require().NoError(err)
	return subject
}

func candidate(t *testing.T, subject model.AuthSubject) model.Profile {
	t.Helper()
	profile, err := model.NewProfile(model.NewProfileParams{AuthUserID: subject, DisplayName: model.DefaultDisplayName})
	require.NoError(t, err)
	return profile
}

func (s *profileSuite) TestStableMappingPreservesUserDisplayName() {
	t := s.T()
	subject := s.subject()
	repo := identitydb.NewProfileRepository(s.resolver)
	first, err := repo.Resolve(t.Context(), candidate(t, subject))
	require.NoError(t, err)
	require.False(t, first.CreatedAt().IsZero())
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.profiles SET display_name='Chosen name',updated_at=clock_timestamp() WHERE id=?", first.ID())
	require.NoError(t, err)
	second, err := repo.Resolve(t.Context(), candidate(t, subject))
	require.NoError(t, err)
	require.Equal(t, first.ID(), second.ID())
	require.Equal(t, "Chosen name", second.DisplayName())
	require.Equal(t, first.CreatedAt(), second.CreatedAt())
	other, err := repo.Resolve(t.Context(), candidate(t, s.subject()))
	require.NoError(t, err)
	require.NotEqual(t, first.ID(), other.ID())
}

func (s *profileSuite) TestConcurrentFirstLoginCreatesOneProfile() {
	t := s.T()
	subject := s.subject()
	repo := identitydb.NewProfileRepository(s.resolver)
	const count = 8
	type result struct {
		profile model.Profile
		err     error
	}
	results := make(chan result, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range count {
		value := candidate(t, subject)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			p, err := repo.Resolve(t.Context(), value)
			results <- result{p, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var profileID string
	for result := range results {
		require.NoError(t, result.err)
		if profileID == "" {
			profileID = result.profile.ID()
		}
		require.Equal(t, profileID, result.profile.ID())
	}
	var rows int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.profiles").Scan(t.Context(), &rows))
	require.Equal(t, 1, rows)
}

func (s *profileSuite) TestMissingAndDeletedSubjectsCannotResolve() {
	t := s.T()
	repo := identitydb.NewProfileRepository(s.resolver)
	_, err := repo.Resolve(t.Context(), candidate(t, model.AuthSubject(uuid.NewString())))
	require.ErrorIs(t, err, model.ErrUnauthenticated)
	subject := s.subject()
	profile, err := repo.Resolve(t.Context(), candidate(t, subject))
	require.NoError(t, err)
	_, err = s.admin.ExecContext(t.Context(), "DELETE FROM auth.users WHERE id=?::uuid", subject)
	require.NoError(t, err)
	_, err = repo.Resolve(t.Context(), candidate(t, subject))
	require.ErrorIs(t, err, model.ErrUnauthenticated)
	var preserved bool
	require.NoError(t, s.admin.NewRaw("SELECT auth_user_id IS NULL FROM handdraw.profiles WHERE id=?", profile.ID()).Scan(t.Context(), &preserved))
	require.True(t, preserved)
}

func (s *profileSuite) TestTombstonedProfileCannotBeResurrected() {
	t := s.T()
	subject := s.subject()
	repo := identitydb.NewProfileRepository(s.resolver)
	profile, err := repo.Resolve(t.Context(), candidate(t, subject))
	require.NoError(t, err)
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.profiles SET deleted_at=clock_timestamp() WHERE id=?", profile.ID())
	require.NoError(t, err)
	_, err = repo.Resolve(t.Context(), candidate(t, subject))
	require.ErrorIs(t, err, model.ErrUnauthenticated)
	var rows int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.profiles WHERE deleted_at IS NOT NULL").Scan(t.Context(), &rows))
	require.Equal(t, 1, rows)
}

func (s *profileSuite) TestResolverRoleCannotAccessTablesDirectly() {
	t := s.T()
	require.NoError(t, identitydb.NewProfileRepository(s.resolver).Check(t.Context()))
	require.ErrorIs(t, identitydb.NewProfileRepository(s.admin).Check(t.Context()), model.ErrUnavailable)
	for _, query := range []string{"SELECT * FROM handdraw.profiles", "UPDATE handdraw.profiles SET display_name='Denied'", "DELETE FROM handdraw.profiles", "INSERT INTO handdraw.profiles(id) VALUES ('denied')", "SELECT * FROM auth.users"} {
		_, err := s.resolver.ExecContext(t.Context(), query)
		require.Error(t, err, query)
	}
	var publicGrant bool
	require.NoError(t, s.admin.NewRaw(`SELECT EXISTS(SELECT 1 FROM pg_proc p, LATERAL aclexplode(p.proacl) a WHERE p.oid='handdraw.resolve_profile(text,uuid,text)'::regprocedure AND a.grantee=0 AND a.privilege_type='EXECUTE')`).Scan(t.Context(), &publicGrant))
	require.False(t, publicGrant)
}

func (s *profileSuite) TestMeThroughHTTPProviderAndRestrictedDatabase() {
	t := s.T()
	subject := s.subject()
	key := []byte("integration-signing-key")
	var failProvider atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failProvider.Load() {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path != "/auth/v1/user" || r.Header.Get("apikey") != "test-public-key" {
			w.WriteHeader(400)
			return
		}
		_, err := jwt.Parse(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), func(*jwt.Token) (any, error) { return key, nil }, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": subject, "email": "developer@example.com"})
	}))
	defer provider.Close()
	verifier, err := supabase.New(supabase.Config{ProjectURL: provider.URL, PublishableKey: "test-public-key"})
	require.NoError(t, err)
	handler := identityapi.New(service.New(verifier, identitydb.NewProfileRepository(s.resolver)))
	// Reserve a loopback address; Server owns its listener and shutdown lifecycle.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	server, err := fx.New(fx.Params{Config: fx.Config{Address: address}, Routes: func(router fiber.Router) { handler.Register(router.Group("/v1")) }})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(15 * time.Second):
			t.Error("server shutdown timed out")
		}
	})
	client := &http.Client{Timeout: 2 * time.Second}
	require.Eventually(t, func() bool {
		response, err := client.Get("http://" + address + "/livez")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200
	}, 5*time.Second, 10*time.Millisecond)
	claims := jwt.MapClaims{"iss": provider.URL + "/auth/v1", "aud": "authenticated", "role": "authenticated", "sub": string(subject), "exp": time.Now().Add(time.Hour).Unix()}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	require.NoError(t, err)
	var stableID string
	for _, tc := range []struct {
		name          string
		authorization []string
		status        int
		providerDown  bool
	}{
		{name: "missing", status: 401},
		{name: "malformed", authorization: []string{"Basic secret"}, status: 401},
		{name: "duplicate", authorization: []string{"Bearer " + token, "Bearer " + token}, status: 401},
		{name: "authenticated", authorization: []string{"Bearer " + token}, status: 200},
		{name: "repeated login", authorization: []string{"Bearer " + token}, status: 200},
		{name: "provider unavailable", authorization: []string{"Bearer " + token}, status: 503, providerDown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failProvider.Store(tc.providerDown)
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+address+"/v1/me", nil)
			require.NoError(t, err)
			for _, header := range tc.authorization {
				request.Header.Add("Authorization", header)
			}
			response, err := client.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, tc.status, response.StatusCode, string(body))
			require.Contains(t, response.Header.Get("Cache-Control"), "no-store")
			var envelope fx.Response[dto.Me]
			require.NoError(t, json.Unmarshal(body, &envelope))
			require.NotEmpty(t, envelope.Meta.RequestID)
			require.NotContains(t, string(body), token)
			require.NotContains(t, string(body), string(subject))
			if tc.status == 200 {
				require.True(t, envelope.Success)
				require.NotNil(t, envelope.Result)
				require.Nil(t, envelope.Error)
				require.Equal(t, "developer@example.com", envelope.Result.Email)
				require.True(t, strings.HasPrefix(envelope.Result.ID, model.ProfileIDPrefix+"_"))
				if stableID == "" {
					stableID = envelope.Result.ID
				}
				require.Equal(t, stableID, envelope.Result.ID)
			} else {
				require.False(t, envelope.Success)
				require.Nil(t, envelope.Result)
				require.NotNil(t, envelope.Error)
				expected := "unauthenticated"
				if tc.status == 503 {
					expected = "identity_unavailable"
				}
				require.Equal(t, expected, envelope.Error.Code)
			}
		})
	}
}
