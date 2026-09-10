//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	boardapi "github.com/chai-rs/handdraw-server/app/board_management/inbound/api"
	boardquery "github.com/chai-rs/handdraw-server/app/board_management/infra/db"
	boardinput "github.com/chai-rs/handdraw-server/app/board_management/model"
	boardworkflow "github.com/chai-rs/handdraw-server/app/board_management/service"
	membershipapi "github.com/chai-rs/handdraw-server/app/membership/inbound/api"
	membershipdb "github.com/chai-rs/handdraw-server/app/membership/infra/db"
	membershipservice "github.com/chai-rs/handdraw-server/app/membership/service"
	boardservice "github.com/chai-rs/handdraw-server/internal/board/service"
	jobdb "github.com/chai-rs/handdraw-server/internal/job/infra/db"

	_ "embed"

	accessdb "github.com/chai-rs/handdraw-server/app/access/infra/db"
	access "github.com/chai-rs/handdraw-server/app/access/model"
	accessservice "github.com/chai-rs/handdraw-server/app/access/service"
	onboardingdb "github.com/chai-rs/handdraw-server/app/onboarding/infra/db"
	onboardingservice "github.com/chai-rs/handdraw-server/app/onboarding/service"
	workspaceapi "github.com/chai-rs/handdraw-server/app/workspace_management/inbound/api"
	workflow "github.com/chai-rs/handdraw-server/app/workspace_management/service"
	boarddb "github.com/chai-rs/handdraw-server/internal/board/infra/db"
	board "github.com/chai-rs/handdraw-server/internal/board/model"
	documentcodec "github.com/chai-rs/handdraw-server/internal/document/infra/ygo"
	documentservice "github.com/chai-rs/handdraw-server/internal/document/service"
	idemdb "github.com/chai-rs/handdraw-server/internal/idempotency/infra/db"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	identitydb "github.com/chai-rs/handdraw-server/internal/identity/infra/db"
	"github.com/chai-rs/handdraw-server/internal/identity/infra/supabase"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	identityservice "github.com/chai-rs/handdraw-server/internal/identity/service"
	"github.com/chai-rs/handdraw-server/internal/testsupport"
	workspacedb "github.com/chai-rs/handdraw-server/internal/workspace/infra/db"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	workspaceservice "github.com/chai-rs/handdraw-server/internal/workspace/service"
	bunx "github.com/chai-rs/handdraw-server/pkg/bun"
	"github.com/chai-rs/handdraw-server/pkg/cursor"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
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

//go:embed testdata/auth.sql
var authFixture string

type accessSuite struct {
	suite.Suite
	admin, request, resolver, cleanup *bun.DB
}
type (
	user     struct{ id, subject string }
	scenario struct {
		workspace                       string
		owner, editor, viewer, outsider user
	}
)

func TestAccessSuite(t *testing.T) { suite.Run(t, new(accessSuite)) }

// SetupSuite applies production migrations after only an external Auth stand-in, then separates runtime credentials.
func (s *accessSuite) SetupSuite() {
	t := s.T()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	database, err := postgres.Run(ctx, "postgres:17", postgres.WithDatabase("handdraw_access_test"), postgres.WithUsername("access_admin"), postgres.WithPassword("local_test_only"), postgres.BasicWaitStrategies(), testcontainers.WithHostConfigModifier(func(c *container.HostConfig) {
		for port, bindings := range c.PortBindings {
			for i := range bindings {
				bindings[i].HostIP = netip.MustParseAddr("127.0.0.1")
			}
			c.PortBindings[port] = bindings
		}
	}))
	testcontainers.CleanupContainer(t, database)
	require.NoError(t, err)
	dsn, err := database.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	s.admin = s.open(t, dsn)
	_, err = s.admin.ExecContext(ctx, authFixture)
	require.NoError(t, err)
	testsupport.Migrate(t, dsn, "up")
	for role, capability := range map[string]string{"access_request": "handdraw_request", "access_resolver": "handdraw_identity_resolver", "access_cleanup": "handdraw_cleanup_worker"} {
		_, err = s.admin.ExecContext(ctx, "CREATE ROLE ? LOGIN PASSWORD 'local_test_only' IN ROLE ?", bun.Ident(role), bun.Ident(capability))
		require.NoError(t, err)
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		u.User = url.UserPassword(role, "local_test_only")
		db := s.open(t, u.String())
		if role == "access_request" {
			s.request = db
		} else if role == "access_cleanup" {
			s.cleanup = db
		} else {
			s.resolver = db
		}
	}
}

func (s *accessSuite) open(t *testing.T, dsn string) *bun.DB {
	t.Helper()
	db, err := (bunx.PGConfig{URL: dsn}).New(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func (s *accessSuite) user(t *testing.T) user {
	t.Helper()
	subject := uuid.NewString()
	_, err := s.admin.ExecContext(t.Context(), "INSERT INTO auth.users(id) VALUES (?::uuid)", subject)
	require.NoError(t, err)
	candidate, err := identity.NewProfile(identity.NewProfileParams{AuthUserID: identity.AuthSubject(subject), DisplayName: "Developer"})
	require.NoError(t, err)
	p, err := identitydb.NewProfileRepository(s.resolver).Resolve(t.Context(), candidate)
	require.NoError(t, err)
	return user{id: p.ID(), subject: subject}
}

func (s *accessSuite) setup(t *testing.T) scenario {
	t.Helper()
	f := scenario{owner: s.user(t), editor: s.user(t), viewer: s.user(t), outsider: s.user(t)}
	f.workspace = s.seedWorkspace(t, f.owner, "team")
	_, err := s.admin.ExecContext(t.Context(), "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'editor'),(?,?,'viewer')", f.workspace, f.editor.id, f.workspace, f.viewer.id)
	require.NoError(t, err)
	return f
}

func (s *accessSuite) seedWorkspace(t *testing.T, owner user, kind string) string {
	t.Helper()
	id, err := resourceid.New(workspace.WorkspaceIDPrefix)
	require.NoError(t, err)
	tx, err := s.admin.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(t.Context(), "INSERT INTO handdraw.workspaces(id,kind,name,owner_user_id) VALUES (?,?,'Architecture',?)", id, kind, owner.id)
	require.NoError(t, err)
	_, err = tx.ExecContext(t.Context(), "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'owner')", id, owner.id)
	require.NoError(t, err)
	plan, seats := "team", 5
	if kind == "personal" {
		plan, seats = "cloud", 1
	}
	_, err = tx.ExecContext(t.Context(), "INSERT INTO handdraw.subscriptions(workspace_id,plan,billing_interval,status,paid_seats,trial_ends_at) VALUES (?,?,'month','trialing',?,clock_timestamp()+interval '7 days')", id, plan, seats)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return id
}

func (s *accessSuite) decision(t *testing.T, actor, id string) (access.Decision, error) {
	t.Helper()
	var d access.Decision
	err := rlstx.Run(t.Context(), s.request, actor, func(ctx context.Context) error {
		var err error
		d, err = accessservice.New(accessdb.New()).Resolve(ctx, access.Target{WorkspaceID: id})
		return err
	})
	return d, err
}

// TestPolicyUsesCurrentMembershipAndEntitlement verifies live actor isolation, retained reads and revocation.
func (s *accessSuite) TestPolicyUsesCurrentMembershipAndEntitlement() {
	t := s.T()
	f := s.setup(t)
	for _, tc := range []struct {
		u    user
		edit bool
	}{{f.owner, true}, {f.editor, true}, {f.viewer, false}} {
		d, err := s.decision(t, tc.u.id, f.workspace)
		require.NoError(t, err)
		require.True(t, d.Capabilities.CanRead)
		require.Equal(t, tc.edit, d.Capabilities.CanEditContent)
		require.Equal(t, tc.edit, d.CanInsertPremium)
		require.True(t, d.Capabilities.CanComment)
	}
	_, err := s.decision(t, f.outsider.id, f.workspace)
	require.ErrorIs(t, err, access.ErrNotFound)
	for _, change := range []string{"status='active',paid_through_at=clock_timestamp()+interval '1 month',last_successful_payment_at=clock_timestamp()", "status='grace_period',renewal_obligation_id='local-renewal',renewal_failure_at=clock_timestamp(),grace_ends_at=clock_timestamp()+interval '6 days'"} {
		_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET "+change+" WHERE workspace_id=?", f.workspace)
		require.NoError(t, err)
		d, err := s.decision(t, f.editor.id, f.workspace)
		require.NoError(t, err)
		require.True(t, d.Capabilities.CanEditContent)
	}
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='ended',access_expires_at=clock_timestamp() WHERE workspace_id=?", f.workspace)
	require.NoError(t, err)
	d, err := s.decision(t, f.owner.id, f.workspace)
	require.NoError(t, err)
	require.True(t, d.Capabilities.CanRead)
	require.False(t, d.Capabilities.CanEditContent)
	require.False(t, d.CanInsertPremium)
	_, err = s.admin.ExecContext(t.Context(), "DELETE FROM handdraw.workspace_members WHERE workspace_id=? AND user_id=?", f.workspace, f.editor.id)
	require.NoError(t, err)
	_, err = s.decision(t, f.editor.id, f.workspace)
	require.ErrorIs(t, err, access.ErrNotFound)
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET access_expires_at=clock_timestamp()-interval '91 days' WHERE workspace_id=?", f.workspace)
	require.NoError(t, err)
	d, err = s.decision(t, f.owner.id, f.workspace)
	require.NoError(t, err)
	require.False(t, d.Capabilities.CanRead)
}

// TestPublicProjectionAndRoleChecksKeepBillingCredentialsPrivate proves members cannot read provider rows or write entitlement.
func (s *accessSuite) TestPublicProjectionAndRoleChecksKeepBillingCredentialsPrivate() {
	t := s.T()
	f := s.setup(t)
	require.NoError(t, accessdb.CheckRequestPool(t.Context(), s.request))
	require.Error(t, accessdb.CheckRequestPool(t.Context(), s.admin))
	require.Error(t, accessdb.CheckRequestPool(t.Context(), s.resolver))
	_, err := accessdb.New().Load(t.Context(), access.Target{WorkspaceID: f.workspace})
	require.ErrorIs(t, err, rlstx.ErrMissingTransaction)
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.editor.id, func(ctx context.Context) error {
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		var count int
		err = tx.NewRaw("SELECT count(*) FROM handdraw.subscriptions WHERE workspace_id=?", f.workspace).Scan(ctx, &count)
		require.Zero(t, count)
		return err
	}))
	for _, q := range []string{"UPDATE handdraw.subscriptions SET status='active'", "UPDATE handdraw.workspace_members SET role='owner'", "UPDATE handdraw.workspaces SET owner_user_id=owner_user_id", "UPDATE handdraw.workspaces SET kind='personal'", "SELECT * FROM auth.users"} {
		err := rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
			tx, err := rlstx.Current(ctx)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, q)
			return err
		})
		require.Error(t, err, q)
	}
	for _, u := range []user{f.editor, f.viewer} {
		require.NoError(t, rlstx.Run(t.Context(), s.request, u.id, func(ctx context.Context) error {
			tx, err := rlstx.Current(ctx)
			if err != nil {
				return err
			}
			result, err := tx.ExecContext(ctx, "UPDATE handdraw.workspaces SET name='Denied',revision=revision+1 WHERE id=?", f.workspace)
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			require.Zero(t, n)
			return err
		}))
	}
}

// TestConcurrentRenamesCommitOnlyOneRevision exercises database serialization with independent transactions.
func (s *accessSuite) TestConcurrentRenamesCommitOnlyOneRevision() {
	t := s.T()
	f := s.setup(t)
	var group sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []workspace.Name{"Allocation", "Sizing"} {
		group.Go(func() {
			results <- rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
				_, err := workspaceservice.New(workspacedb.New()).Rename(ctx, f.workspace, name, 1)
				return err
			})
		})
	}
	group.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, workspace.ErrRevisionConflict)
			conflict++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
}

// TestEntitlementChangeWinsBeforeWaitingRename prevents a stale authorization decision from becoming a write.
func (s *accessSuite) TestEntitlementChangeWinsBeforeWaitingRename() {
	t := s.T()
	f := s.setup(t)
	tx, err := s.admin.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='ended',access_expires_at=clock_timestamp() WHERE workspace_id=?", f.workspace)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- rlstx.Run(ctx, s.request, f.owner.id, func(ctx context.Context) error {
			_, err := workspacedb.New().Rename(ctx, f.workspace, "Rejected", 1)
			return err
		})
	}()
	require.Eventually(t, func() bool {
		var blocked bool
		err := s.admin.NewRaw("SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE usename='access_request' AND wait_event_type='Lock')").Scan(t.Context(), &blocked)
		return err == nil && blocked
	}, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, tx.Commit())
	require.Error(t, <-done)
	d, err := s.decision(t, f.owner.id, f.workspace)
	require.NoError(t, err)
	require.Equal(t, "Architecture", d.Facts.Workspace.Name)
}

func (s *accessSuite) http(t *testing.T, accounts ...map[string]user) (string, func(user) string) {
	t.Helper()
	key := []byte("local-integration-signing-key")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(accounts) > 0 && localAuth(w, r, accounts[0], key) {
			return
		}
		if r.URL.Path != "/auth/v1/user" || r.Header.Get("apikey") != "local-public-key" {
			w.WriteHeader(400)
			return
		}
		token, err := jwt.Parse(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), func(*jwt.Token) (any, error) { return key, nil }, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil {
			w.WriteHeader(401)
			return
		}
		subject, err := token.Claims.GetSubject()
		if err != nil {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": subject, "email": "developer@example.test"})
	}))
	t.Cleanup(provider.Close)
	if len(accounts) > 0 {
		localProviderURL = provider.URL
	}
	verifier, err := supabase.New(supabase.Config{ProjectURL: provider.URL, PublishableKey: "local-public-key"})
	require.NoError(t, err)
	identityService := identityservice.New(verifier, identitydb.NewProfileRepository(s.resolver))
	session := accessservice.NewSession(identityService, rlstx.NewRunner(s.request))
	policy := accessservice.New(accessdb.New())
	app := workflow.New(workspaceservice.New(workspacedb.New()), policy)
	cursors, err := cursor.New([]byte(strings.Repeat("integration-key-", 3)))
	require.NoError(t, err)
	handler := workspaceapi.New(session, app, cursors).WithOnboarding(onboardingservice.New(onboardingdb.New(), idemdb.New(), policy))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	httpConfig := fx.Config{Address: address}
	if len(accounts) > 0 {
		httpConfig.CORS = fx.CORSConfig{Enabled: true, AllowOrigins: []string{"http://127.0.0.1:5175"}}
	}
	server, err := fx.New(fx.Params{Config: httpConfig, Routes: func(router fiber.Router) {
		handler.Register(router.Group("/v1"))
		tokens, err := membershipservice.NewTokens([]byte(strings.Repeat("membership-local-key-", 3)))
		require.NoError(t, err)
		members := membershipservice.New(membershipdb.New(), policy, tokens, idemdb.New())
		membershipapi.New(session, members, cursors).Register(router.Group("/v1"))
		boardapi.New(session, s.boards(), cursors, true).WithSharedBoards(members).Register(router.Group("/v1"))
	}})
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
			t.Error("HTTP shutdown timed out")
		}
	})
	client := http.Client{Timeout: 2 * time.Second}
	base := "http://" + address
	require.Eventually(t, func() bool {
		r, err := client.Get(base + "/livez")
		if err != nil {
			return false
		}
		defer r.Body.Close()
		return r.StatusCode == 200
	}, 5*time.Second, 10*time.Millisecond)
	return base, func(u user) string {
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": provider.URL + "/auth/v1", "aud": "authenticated", "role": "authenticated", "sub": u.subject, "exp": time.Now().Add(time.Hour).Unix()}).SignedString(key)
		require.NoError(t, err)
		return token
	}
}

func request(t *testing.T, method, url, token, body, etag string, keys ...string) (int, map[string]any, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("X-User-ID", "usr_0ujtsYcgvSTl8PAuAdqWYSMnLOv")
	req.Header.Set("Content-Type", "application/json")
	if len(keys) > 0 {
		req.Header.Set("Idempotency-Key", keys[0])
	}
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	client := http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	var result map[string]any
	if len(data) > 0 {
		require.NoError(t, json.Unmarshal(data, &result))
	}
	return response.StatusCode, result, response.Header
}

// TestWorkspaceHTTPAuthenticatesBeforeActorScope validates the actual provider, resolver, transaction, workflow and Fiber envelope.
func (s *accessSuite) TestWorkspaceHTTPAuthenticatesBeforeActorScope() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	for _, tc := range []struct {
		name, method, token, body, etag string
		status                          int
	}{
		{"missing identity", "GET", "", "", "", 401},
		{"owner read", "GET", token(f.owner), "", "", 200},
		{"viewer read", "GET", token(f.viewer), "", "", 200},
		{"outsider", "GET", token(f.outsider), "", "", 404},
		{"Viewer spoofed actor", "PATCH", token(f.viewer), `{"name":"Spoofed"}`, `"1"`, 403},
		{"Editor cannot rename", "PATCH", token(f.editor), `{"name":"No"}`, `"1"`, 403},
		{"missing precondition", "PATCH", token(f.owner), `{"name":"No"}`, "", 428},
		{"unknown field", "PATCH", token(f.owner), `{"name":"No","role":"owner"}`, `"1"`, 400},
		{"valid rename", "PATCH", token(f.owner), `{"name":"Allocation"}`, `"1"`, 200},
		{"stale revision", "PATCH", token(f.owner), `{"name":"Stale"}`, `"1"`, 412},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, body, headers := request(t, tc.method, base+"/v1/workspaces/"+f.workspace, tc.token, tc.body, tc.etag)
			require.Equal(t, tc.status, status, body)
			require.NotEmpty(t, headers.Get("X-Request-ID"))
			if status == 200 {
				require.Equal(t, true, body["success"])
				result := body["result"].(map[string]any)
				require.IsType(t, "", result["revision"])
				require.Equal(t, f.workspace, result["id"])
				require.NotEmpty(t, headers.Get("ETag"))
			}
		})
	}
}

// TestWorkspacePaginationRejectsAnotherActorsCursor binds the continuation to identity even when both users share the workspace.
func (s *accessSuite) TestWorkspacePaginationRejectsAnotherActorsCursor() {
	t := s.T()
	f := s.setup(t)
	s.seedWorkspace(t, f.owner, "personal")
	s.seedWorkspace(t, f.owner, "personal")
	base, token := s.http(t)
	status, body, _ := request(t, "GET", base+"/v1/workspaces?limit=1", token(f.owner), "", "")
	require.Equal(t, 200, status, body)
	continuation := body["meta"].(map[string]any)["pagination"].(map[string]any)["next_cursor"].(string)
	status, _, _ = request(t, "GET", base+"/v1/workspaces?limit=1&cursor="+url.QueryEscape(continuation), token(f.owner), "", "")
	require.Equal(t, 200, status)
	status, body, _ = request(t, "GET", base+"/v1/workspaces?limit=1&cursor="+url.QueryEscape(continuation), token(f.viewer), "", "")
	require.Equal(t, 400, status)
	require.Equal(t, "invalid_cursor", body["error"].(map[string]any)["code"])
}

// TestWorkspaceMutationRollbackRetainsCommittedMetadata prevents failed workflows from leaking a rename.
func (s *accessSuite) TestWorkspaceMutationRollbackRetainsCommittedMetadata() {
	t := s.T()
	f := s.setup(t)
	err := rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		_, err := workspacedb.New().Rename(ctx, f.workspace, "Uncommitted", 1)
		if err != nil {
			return err
		}
		return access.ErrDenied
	})
	require.ErrorIs(t, err, access.ErrDenied)
	d, err := s.decision(t, f.owner.id, f.workspace)
	require.NoError(t, err)
	require.Equal(t, "Architecture", d.Facts.Workspace.Name)
	require.EqualValues(t, 1, d.Facts.Workspace.Revision)
}

// TestBoardPolicyIncludesLifecycleAndWorkspaceIsolation checks the board target against persisted content and membership.
func (s *accessSuite) TestBoardPolicyIncludesLifecycleAndWorkspaceIsolation() {
	t := s.T()
	f := s.setup(t)
	for _, status := range []board.Status{board.StatusActive, board.StatusInitializing} {
		b, err := board.NewBoard(board.NewBoardParams{WorkspaceID: f.workspace, CreatedBy: f.owner.id, Name: "Allocation", Status: status})
		require.NoError(t, err)
		initial, err := documentservice.NewInitialBuilder(documentcodec.Codec{}).Build(b.ID(), "empty")
		require.NoError(t, err)
		require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
			_, err := boarddb.NewBoardRepository().Create(ctx, b, board.InitialDocument{State: initial.State, SchemaVersion: initial.SchemaVersion})
			return err
		}))
		for _, tc := range []struct {
			u    user
			edit bool
		}{{f.owner, true}, {f.editor, true}, {f.viewer, false}, {f.outsider, false}} {
			var d access.Decision
			err = rlstx.Run(t.Context(), s.request, tc.u.id, func(ctx context.Context) error {
				var loadErr error
				d, loadErr = accessservice.New(accessdb.New()).Resolve(ctx, access.Target{BoardID: b.ID()})
				return loadErr
			})
			if tc.u == f.outsider {
				require.ErrorIs(t, err, access.ErrNotFound)
				continue
			}
			require.NoError(t, err)
			require.Equal(t, status == board.StatusActive, d.Capabilities.CanRead)
			require.Equal(t, status == board.StatusActive && tc.edit, d.Capabilities.CanEditContent)
			require.Equal(t, status == board.StatusActive && tc.edit, d.CanInsertPremium)
		}
	}
}

// TestOnboardingRetriesRemainAtomicAndUnpaid proves bootstrap cannot create a subscription or duplicate its Owner.
func (s *accessSuite) TestOnboardingRetriesRemainAtomicAndUnpaid() {
	t := s.T()
	u := s.user(t)
	key := uuid.NewString()
	svc := onboardingservice.New(onboardingdb.New(), idemdb.New(), accessservice.New(accessdb.New()))
	create := func(name string) (access.Decision, error) {
		var d access.Decision
		err := rlstx.Run(t.Context(), s.request, u.id, func(ctx context.Context) error {
			var err error
			d, err = svc.Create(ctx, name, "personal", key)
			return err
		})
		return d, err
	}
	d, err := create("Personal")
	require.NoError(t, err)
	again, err := create("Personal")
	require.NoError(t, err)
	require.Equal(t, d.Facts.Workspace.ID, again.Facts.Workspace.ID)
	require.Equal(t, "unavailable", d.Facts.Entitlement.Mode)
	require.False(t, d.Capabilities.CanEditContent)
	require.Equal(t, workspace.Owner, d.Facts.Member.Role)
	_, err = create("Changed")
	require.ErrorIs(t, err, idem.ErrConflict)
	var count int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.workspaces WHERE owner_user_id=?", u.id).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.subscriptions WHERE workspace_id=?", d.Facts.Workspace.ID).Scan(t.Context(), &count))
	require.Zero(t, count)
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.workspace_usage WHERE workspace_id=?", d.Facts.Workspace.ID).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
	err = rlstx.Run(t.Context(), s.request, u.id, func(ctx context.Context) error {
		_, err := svc.Create(ctx, "Rollback", "team", uuid.NewString())
		if err != nil {
			return err
		}
		return access.ErrDenied
	})
	require.ErrorIs(t, err, access.ErrDenied)
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.workspaces WHERE owner_user_id=?", u.id).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
}

// TestOnboardingConcurrentKeyAndExpiry verifies one committed workspace and bounded processing retries.
func (s *accessSuite) TestOnboardingConcurrentKeyAndExpiry() {
	t := s.T()
	u := s.user(t)
	key := uuid.NewString()
	svc := onboardingservice.New(onboardingdb.New(), idemdb.New(), accessservice.New(accessdb.New()))
	held, err := s.request.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = held.Rollback() }()
	_, err = held.ExecContext(t.Context(), "SELECT pg_advisory_xact_lock(hashtextextended(?,0))", u.id+"/workspace.create/"+key)
	require.NoError(t, err)
	create := func() (access.Decision, error) {
		var d access.Decision
		err := rlstx.Run(t.Context(), s.request, u.id, func(ctx context.Context) error {
			var err error
			d, err = svc.Create(ctx, "Team", "team", key)
			return err
		})
		return d, err
	}
	_, err = create()
	require.ErrorIs(t, err, idem.ErrProcessing)
	require.NoError(t, held.Rollback())
	var wg sync.WaitGroup
	results := make(chan error, 5)
	for range 5 {
		wg.Go(func() { _, err := create(); results <- err })
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			require.ErrorIs(t, err, idem.ErrProcessing)
		}
	}
	first, err := create()
	require.NoError(t, err)
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.idempotency_records SET created_at=clock_timestamp()-interval '2 days',expires_at=clock_timestamp()-interval '1 day' WHERE actor_user_id=?", u.id)
	require.NoError(t, err)
	next, err := create()
	require.NoError(t, err)
	require.NotEqual(t, first.Facts.Workspace.ID, next.Facts.Workspace.ID)
}

func (s *accessSuite) boards() *boardworkflow.Service {
	return boardworkflow.New(boardservice.NewBoardService(boarddb.NewBoardRepository()), boardservice.NewProjectService(boarddb.NewProjectRepository()), documentservice.NewInitialBuilder(documentcodec.Codec{}), accessservice.New(accessdb.New()), boardquery.New(), idemdb.New(), jobdb.New())
}

// TestBoardHTTPLifecycleAndAtomicDocument covers real authenticated creation, grouping, revision checks and cleanup.
func (s *accessSuite) TestBoardHTTPLifecycleAndAtomicDocument() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	w := base + "/v1/workspaces/" + f.workspace
	key := uuid.NewString()
	status, data, _ := request(t, "POST", w+"/projects", token(f.owner), `{"name":"System"}`, "", uuid.NewString())
	require.Equal(t, 201, status, data)
	project := data["result"].(map[string]any)["id"].(string)
	payload := `{"name":"Allocation","initialization":"get_started","project_id":"` + project + `"}`
	status, data, headers := request(t, "POST", w+"/boards", token(f.editor), payload, "", key)
	require.Equal(t, 201, status, data)
	require.Equal(t, `"1"`, headers.Get("ETag"))
	b := data["result"].(map[string]any)["id"].(string)
	status, replay, _ := request(t, "POST", w+"/boards", token(f.editor), payload, "", key)
	require.Equal(t, 201, status, replay)
	require.Equal(t, b, replay["result"].(map[string]any)["id"])
	status, data, _ = request(t, "POST", w+"/boards", token(f.editor), `{"name":"Other","initialization":"empty","project_id":null}`, "", key)
	require.Equal(t, 409, status, data)
	for _, u := range []user{f.owner, f.editor, f.viewer} {
		status, data, _ = request(t, "GET", base+"/v1/boards/"+b, token(u), "", "")
		require.Equal(t, 200, status, data)
		require.Equal(t, u != f.viewer, data["result"].(map[string]any)["capabilities"].(map[string]any)["can_edit_content"])
	}
	status, data, _ = request(t, "GET", base+"/v1/boards/"+b, token(f.outsider), "", "")
	require.Equal(t, 404, status, data)
	status, data, _ = request(t, "PATCH", base+"/v1/boards/"+b, token(f.viewer), `{"name":"Spoof"}`, `"1"`)
	require.Equal(t, 403, status, data)
	status, data, _ = request(t, "DELETE", base+"/v1/projects/"+project, token(f.owner), "", `"1"`)
	require.Equal(t, 409, status, data)
	status, data, _ = request(t, "PATCH", base+"/v1/boards/"+b, token(f.editor), `{"name":"Renamed","project_id":null}`, `"1"`)
	require.Equal(t, 200, status, data)
	require.Nil(t, data["result"].(map[string]any)["project_id"])
	status, data, _ = request(t, "PATCH", base+"/v1/boards/"+b, token(f.editor), `{"name":"Stale"}`, `"1"`)
	require.Equal(t, 412, status, data)
	for range 2 {
		status, data, _ = request(t, "DELETE", base+"/v1/projects/"+project, token(f.owner), "", `"1"`)
		require.Equal(t, 204, status, data)
	}
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.viewer.id, func(ctx context.Context) error {
		state, version, err := s.boards().Document(ctx, b)
		require.NoError(t, err)
		require.Equal(t, 1, version)
		require.Greater(t, len(state), 2)
		return err
	}))
	status, data, _ = request(t, "DELETE", base+"/v1/boards/"+b, token(f.editor), "", `"2"`)
	require.Equal(t, 202, status, data)
	job := data["result"].(map[string]any)["id"]
	status, data, _ = request(t, "DELETE", base+"/v1/boards/"+b, token(f.editor), "", `"2"`)
	require.Equal(t, 202, status, data)
	require.Equal(t, job, data["result"].(map[string]any)["id"])
	var readErr error
	_ = rlstx.Run(t.Context(), s.request, f.viewer.id, func(ctx context.Context) error { _, _, readErr = s.boards().Document(ctx, b); return readErr })
	require.Error(t, readErr)
	worker := jobdb.NewWorker(s.cleanup)
	require.NoError(t, worker.Check(t.Context()))
	require.Error(t, jobdb.NewWorker(s.request).Check(t.Context()))
	n, err := worker.RunOne(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	status, data, _ = request(t, "DELETE", base+"/v1/boards/"+b, token(f.editor), "", `"2"`)
	require.Equal(t, 202, status, data)
	require.Equal(t, "succeeded", data["result"].(map[string]any)["status"])
	var count int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.board_documents WHERE board_id=?", b).Scan(t.Context(), &count))
	require.Zero(t, count)
	status, data, _ = request(t, "POST", w+"/boards", token(f.editor), payload, "", key)
	require.Equal(t, 404, status, data)
}

// TestBoardCreationFailureRollsBackMetadataDocumentAndKey verifies all three writes share a transaction.
func (s *accessSuite) TestBoardCreationFailureRollsBackMetadataDocumentAndKey() {
	t := s.T()
	f := s.setup(t)
	key := uuid.NewString()
	var id string
	err := rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		v, err := s.boards().Create(ctx, f.workspace, key, boardinput.CreateBoard{Name: "Rollback", Initialization: "empty"})
		if err != nil {
			return err
		}
		id = v.Board.ID()
		return access.ErrDenied
	})
	require.ErrorIs(t, err, access.ErrDenied)
	var count int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.boards WHERE id=?", id).Scan(t.Context(), &count))
	require.Zero(t, count)
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		v, err := s.boards().Create(ctx, f.workspace, key, boardinput.CreateBoard{Name: "Rollback", Initialization: "empty"})
		if err != nil {
			return err
		}
		require.NotEqual(t, id, v.Board.ID())
		return nil
	}))
}

// TestBoardHTTPRejectsInvalidInputsAndCrossWorkspaceAssignments checks the wire contract against authorization and strict JSON.
func (s *accessSuite) TestBoardHTTPRejectsInvalidInputsAndCrossWorkspaceAssignments() {
	t := s.T()
	f := s.setup(t)
	other := s.setup(t)
	base, token := s.http(t)
	path := base + "/v1/workspaces/" + f.workspace + "/boards"
	var project board.Project
	require.NoError(t, rlstx.Run(t.Context(), s.request, other.owner.id, func(ctx context.Context) error {
		var err error
		project, err = s.boards().CreateProject(ctx, other.workspace, "Other", uuid.NewString())
		return err
	}))
	for _, tc := range []struct {
		name, body string
		u          user
		key        string
		status     int
	}{
		{"viewer", `{"name":"Denied","initialization":"empty","project_id":null}`, f.viewer, uuid.NewString(), 403},
		{"outsider", `{"name":"Denied","initialization":"empty","project_id":null}`, f.outsider, uuid.NewString(), 404},
		{"foreign project", `{"name":"Denied","initialization":"empty","project_id":"` + project.ID() + `"}`, f.owner, uuid.NewString(), 400},
		{"import", `{"name":"Denied","initialization":"import","project_id":null}`, f.owner, uuid.NewString(), 422},
		{"unknown field", `{"name":"Denied","initialization":"empty","created_by":"fake"}`, f.owner, uuid.NewString(), 400},
		{"missing project field", `{"name":"Denied","initialization":"empty"}`, f.owner, uuid.NewString(), 400},
		{"missing key", `{"name":"Denied","initialization":"empty","project_id":null}`, f.owner, "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, data, _ := request(t, "POST", path, token(tc.u), tc.body, "", tc.key)
			require.Equal(t, tc.status, status, data)
		})
	}
}

// TestCleanupRollsBackFailedAttemptsAndMultipleWorkersDoNotDuplicateDeletion verifies the current atomic worker boundary.
func (s *accessSuite) TestCleanupRollsBackFailedAttemptsAndMultipleWorkersDoNotDuplicateDeletion() {
	t := s.T()
	f := s.setup(t)
	var b string
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		v, err := s.boards().Create(ctx, f.workspace, uuid.NewString(), boardinput.CreateBoard{Name: "Cleanup retry", Initialization: "empty"})
		b = v.Board.ID()
		return err
	}))
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error { _, err := s.boards().Delete(ctx, b, 1); return err }))
	// A local constraint simulates cleanup failure after the document delete; the subtransaction must restore it.
	_, err := s.admin.ExecContext(t.Context(), "CREATE TABLE cleanup_failure_guard(board_id text REFERENCES handdraw.boards(id))")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = s.admin.ExecContext(context.Background(), "DROP TABLE IF EXISTS cleanup_failure_guard") })
	_, err = s.admin.ExecContext(t.Context(), "INSERT INTO cleanup_failure_guard VALUES (?)", b)
	require.NoError(t, err)
	worker := jobdb.NewWorker(s.cleanup)
	n, err := worker.RunOne(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	var count int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.board_documents WHERE board_id=?", b).Scan(t.Context(), &count))
	require.Equal(t, 1, count)
	var status string
	require.NoError(t, s.admin.NewRaw("SELECT status FROM handdraw.board_deletion_jobs WHERE board_id=?", b).Scan(t.Context(), &status))
	require.Equal(t, "queued", status)
	_, err = s.admin.ExecContext(t.Context(), "DELETE FROM cleanup_failure_guard WHERE board_id=?", b)
	require.NoError(t, err)
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.board_deletion_jobs SET retry_at=clock_timestamp() WHERE board_id=?", b)
	require.NoError(t, err)
	results := make(chan int, 2)
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { n, err := jobdb.NewWorker(s.cleanup).RunOne(t.Context()); results <- n; failures <- err })
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	total := 0
	for n := range results {
		total += n
	}
	require.Equal(t, 1, total)
	require.NoError(t, s.admin.NewRaw("SELECT status FROM handdraw.board_deletion_jobs WHERE board_id=?", b).Scan(t.Context(), &status))
	require.Equal(t, "succeeded", status)
}

// TestBoardPaginationBindsActorAndProjectFilter verifies deterministic continuation and scope rejection over HTTP.
func (s *accessSuite) TestBoardPaginationBindsActorAndProjectFilter() {
	t := s.T()
	f := s.setup(t)
	for _, name := range []string{"One", "Two", "Three"} {
		require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
			_, err := s.boards().Create(ctx, f.workspace, uuid.NewString(), boardinput.CreateBoard{Name: name, Initialization: "empty"})
			return err
		}))
	}
	base, token := s.http(t)
	path := base + "/v1/workspaces/" + f.workspace + "/boards?limit=1"
	status, data, _ := request(t, "GET", path, token(f.owner), "", "")
	require.Equal(t, 200, status, data)
	first := data["result"].([]any)[0].(map[string]any)["id"]
	cursor := data["meta"].(map[string]any)["pagination"].(map[string]any)["next_cursor"].(string)
	status, data, _ = request(t, "GET", path+"&cursor="+url.QueryEscape(cursor), token(f.owner), "", "")
	require.Equal(t, 200, status, data)
	require.NotEqual(t, first, data["result"].([]any)[0].(map[string]any)["id"])
	status, data, _ = request(t, "GET", path+"&cursor="+url.QueryEscape(cursor), token(f.viewer), "", "")
	require.Equal(t, 400, status, data)
	status, data, _ = request(t, "GET", path+"&project_id=ungrouped&cursor="+url.QueryEscape(cursor), token(f.owner), "", "")
	require.Equal(t, 400, status, data)
}
