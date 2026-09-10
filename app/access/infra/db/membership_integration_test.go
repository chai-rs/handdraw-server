//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"

	accessdb "github.com/chai-rs/handdraw-server/app/access/infra/db"
	access "github.com/chai-rs/handdraw-server/app/access/model"
	accessservice "github.com/chai-rs/handdraw-server/app/access/service"
	boardinput "github.com/chai-rs/handdraw-server/app/board_management/model"
	membershipdb "github.com/chai-rs/handdraw-server/app/membership/infra/db"
	membership "github.com/chai-rs/handdraw-server/app/membership/model"
	membershipservice "github.com/chai-rs/handdraw-server/app/membership/service"
	idemdb "github.com/chai-rs/handdraw-server/internal/idempotency/infra/db"
	jobdb "github.com/chai-rs/handdraw-server/internal/job/infra/db"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func (s *accessSuite) confirmEmail(t *testing.T, u user, email string, verified bool) {
	t.Helper()
	_, err := s.admin.ExecContext(t.Context(), `UPDATE auth.users SET email=?,email_confirmed_at=CASE WHEN ? THEN clock_timestamp() ELSE NULL END WHERE id=?::uuid`, email, verified, u.subject)
	require.NoError(t, err)
}

func (s *accessSuite) membership(t *testing.T) *membershipservice.Service {
	t.Helper()
	tokens, err := membershipservice.NewTokens([]byte(strings.Repeat("membership-local-key-", 3)))
	require.NoError(t, err)
	return membershipservice.New(membershipdb.New(), accessservice.New(accessdb.New()), tokens, idemdb.New())
}

func invitationToken(t *testing.T, id string) workspace.InvitationToken {
	t.Helper()
	tokens, err := membershipservice.NewTokens([]byte(strings.Repeat("membership-local-key-", 3)))
	require.NoError(t, err)
	token, err := tokens.Issue(id)
	require.NoError(t, err)
	return token
}

func acceptBody(t *testing.T, id string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"token": invitationToken(t, id)})
	require.NoError(t, err)
	return string(raw)
}

// TestMembershipHTTPEnforcesRecipientSeatsAndOwnerInvariant exercises real identity, transport and guarded SQL together.
func (s *accessSuite) TestMembershipHTTPEnforcesRecipientSeatsAndOwnerInvariant() {
	t := s.T()
	f := s.setup(t)
	s.confirmEmail(t, f.outsider, "recipient@example.test", false)
	_, err := s.admin.ExecContext(t.Context(), `UPDATE handdraw.subscriptions SET paid_seats=3 WHERE workspace_id=?`, f.workspace)
	require.NoError(t, err)
	base, token := s.http(t)
	path := base + "/v1/workspaces/" + f.workspace
	key := uuid.NewString()
	payload := `{"email":" Recipient@Example.Test ","role":"editor"}`
	status, data, _ := request(t, "POST", path+"/invitations", token(f.owner), payload, "", key)
	require.Equal(t, 202, status, data)
	result := data["result"].(map[string]any)
	inv := result["invitation"].(map[string]any)
	id := inv["id"].(string)
	require.Equal(t, "pending_integration", result["delivery_status"])
	require.NotContains(t, inv, "token")
	require.NotContains(t, inv, "token_hash")
	status, data, _ = request(t, "POST", path+"/invitations", token(f.owner), payload, "", key)
	require.Equal(t, 202, status, data)
	require.Equal(t, id, data["result"].(map[string]any)["invitation"].(map[string]any)["id"])
	status, data, _ = request(t, "POST", path+"/invitations", token(f.owner), `{"email":"next@example.test","role":"editor"}`, "", uuid.NewString())
	require.Equal(t, 409, status, data)
	status, data, _ = request(t, "POST", base+"/v1/invitations/accept", token(f.viewer), acceptBody(t, id), "")
	require.Equal(t, 403, status, data)
	status, data, _ = request(t, "POST", base+"/v1/invitations/accept", token(f.outsider), acceptBody(t, id), "")
	require.Equal(t, 403, status, data)
	s.confirmEmail(t, f.outsider, "recipient@example.test", true)
	status, data, _ = request(t, "POST", base+"/v1/invitations/accept", token(f.outsider), acceptBody(t, id), "")
	require.Equal(t, 200, status, data)
	require.Equal(t, "editor", data["result"].(map[string]any)["role"])
	status, data, _ = request(t, "POST", base+"/v1/invitations/accept", token(f.outsider), acceptBody(t, id), "")
	require.Equal(t, 410, status, data)
	status, data, _ = request(t, "GET", path+"/members?limit=1", token(f.viewer), "", "")
	require.Equal(t, 200, status, data)
	members := data["result"].([]any)
	require.Len(t, members, 1)
	require.NotContains(t, members[0].(map[string]any), "email")
	next := data["meta"].(map[string]any)["pagination"].(map[string]any)["next_cursor"].(string)
	status, data, _ = request(t, "GET", path+"/members?limit=1&cursor="+url.QueryEscape(next), token(f.viewer), "", "")
	require.Equal(t, 200, status, data)
	status, data, _ = request(t, "GET", path+"/members?cursor="+url.QueryEscape(next), token(f.owner), "", "")
	require.Equal(t, 400, status, data)
	memberPath := path + "/members/" + f.outsider.id
	status, data, headers := request(t, "PATCH", memberPath, token(f.owner), `{"role":"viewer"}`, `"1"`)
	require.Equal(t, 200, status, data)
	require.Equal(t, `"2"`, headers.Get("ETag"))
	decision, err := s.decision(t, f.outsider.id, f.workspace)
	require.NoError(t, err)
	require.False(t, decision.Capabilities.CanEditContent)
	status, data, _ = request(t, "PATCH", memberPath, token(f.owner), `{"role":"editor"}`, `"1"`)
	require.Equal(t, 412, status, data)
	status, data, _ = request(t, "PATCH", path+"/members/"+f.owner.id, token(f.owner), `{"role":"viewer"}`, `"1"`)
	require.Equal(t, 403, status, data)
	status, data, _ = request(t, "DELETE", path+"/members/"+f.owner.id, token(f.owner), "", `"1"`)
	require.Equal(t, 403, status, data)
	status, data, _ = request(t, "POST", path+"/invitations", token(f.editor), `{"email":"next@example.test","role":"viewer"}`, "", uuid.NewString())
	require.Equal(t, 403, status, data)
	for range 2 {
		status, data, _ = request(t, "DELETE", memberPath, token(f.owner), "", `"2"`)
		require.Equal(t, 204, status, data)
	}
	_, err = s.decision(t, f.outsider.id, f.workspace)
	require.ErrorIs(t, err, access.ErrNotFound)
}

// TestBoardGuestOnlySeesItsGrantedBoardAndRevocationClosesHTTP proves guests never acquire workspace membership.
func (s *accessSuite) TestBoardGuestOnlySeesItsGrantedBoardAndRevocationClosesHTTP() {
	t := s.T()
	f := s.setup(t)
	s.confirmEmail(t, f.outsider, "guest@example.test", true)
	personal := s.seedWorkspace(t, f.owner, "personal")
	var boardID, siblingID string
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		v, err := s.boards().Create(ctx, personal, uuid.NewString(), boardinput.CreateBoard{Name: "Shared", Initialization: "get_started"})
		if err != nil {
			return err
		}
		boardID = v.Board.ID()
		v, err = s.boards().Create(ctx, personal, uuid.NewString(), boardinput.CreateBoard{Name: "Private", Initialization: "empty"})
		siblingID = v.Board.ID()
		return err
	}))
	base, token := s.http(t)
	path := base + "/v1/boards/" + boardID
	status, data, _ := request(t, "POST", path+"/invitations", token(f.owner), `{"email":"guest@example.test","role":"viewer"}`, "", uuid.NewString())
	require.Equal(t, 202, status, data)
	id := data["result"].(map[string]any)["invitation"].(map[string]any)["id"].(string)
	status, data, _ = request(t, "POST", base+"/v1/invitations/accept", token(f.outsider), acceptBody(t, id), "")
	require.Equal(t, 200, status, data)
	require.Equal(t, "board_grant", data["result"].(map[string]any)["source"])
	status, data, _ = request(t, "GET", path, token(f.outsider), "", "")
	require.Equal(t, 200, status, data)
	require.False(t, data["result"].(map[string]any)["capabilities"].(map[string]any)["can_edit_content"].(bool))
	for _, target := range []string{"/v1/workspaces/" + personal, "/v1/workspaces/" + personal + "/members", "/v1/boards/" + siblingID} {
		status, data, _ = request(t, "GET", base+target, token(f.outsider), "", "")
		require.Equal(t, 404, status, data)
	}
	status, data, _ = request(t, "GET", base+"/v1/me/shared-boards", token(f.outsider), "", "")
	require.Equal(t, 200, status, data)
	require.Len(t, data["result"].([]any), 1)
	status, data, _ = request(t, "PATCH", path, token(f.outsider), `{"name":"Denied"}`, `"1"`)
	require.Equal(t, 403, status, data)
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.outsider.id, func(ctx context.Context) error {
		_, version, err := s.boards().Document(ctx, boardID)
		require.Equal(t, 1, version)
		if err != nil {
			return err
		}
		tx, err := rlstx.Current(ctx)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE handdraw.board_documents SET revision=revision+1 WHERE board_id=?`, boardID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		require.Zero(t, n)
		return err
	}))
	status, data, _ = request(t, "GET", path+"/guests", token(f.owner), "", "")
	require.Equal(t, 200, status, data)
	require.Len(t, data["result"].([]any), 1)
	for range 2 {
		status, data, _ = request(t, "DELETE", path+"/guests/"+f.outsider.id, token(f.owner), "", "")
		require.Equal(t, 200, status, data)
		require.Nil(t, data["result"].(map[string]any)["effective_access"])
	}
	status, data, _ = request(t, "GET", path, token(f.outsider), "", "")
	require.Equal(t, 404, status, data)
	status, data, _ = request(t, "GET", base+"/v1/me/shared-boards", token(f.outsider), "", "")
	require.Equal(t, 200, status, data)
	require.Empty(t, data["result"])
}

// TestConcurrentAcceptAndPromotionNeverAllocateTheSameSeat proves both paths serialize with pending reservations.
func (s *accessSuite) TestConcurrentAcceptAndPromotionNeverAllocateTheSameSeat() {
	t := s.T()
	f := s.setup(t)
	s.confirmEmail(t, f.outsider, "race@example.test", true)
	_, err := s.admin.ExecContext(t.Context(), `UPDATE handdraw.subscriptions SET paid_seats=3 WHERE workspace_id=?`, f.workspace)
	require.NoError(t, err)
	workflow := s.membership(t)
	var invitation workspace.Invitation
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		var err error
		invitation, err = workflow.Invite(ctx, access.Target{WorkspaceID: f.workspace}, workspace.InviteParams{Email: "race@example.test", Role: workspace.Editor}, uuid.NewString())
		return err
	}))
	token := invitationToken(t, invitation.ID)
	start := make(chan struct{})
	outcomes := make(chan error, 3)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			outcomes <- rlstx.Run(t.Context(), s.request, f.outsider.id, func(ctx context.Context) error { _, err := workflow.Accept(ctx, token); return err })
		})
	}
	wg.Go(func() {
		<-start
		role := workspace.Editor
		outcomes <- rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
			_, err := workflow.ChangeMember(ctx, workspace.RoleChange{WorkspaceID: f.workspace, UserID: f.viewer.id, Role: &role, Revision: 1})
			return err
		})
	})
	close(start)
	wg.Wait()
	close(outcomes)
	success, gone, capacity := 0, 0, 0
	for err := range outcomes {
		switch {
		case err == nil:
			success++
		case errors.Is(err, workspace.ErrInvitationGone):
			gone++
		case errors.Is(err, workspace.ErrCapacity):
			capacity++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, gone)
	require.Equal(t, 1, capacity)
	var allocated int
	require.NoError(t, s.admin.NewRaw(`SELECT count(*) FROM handdraw.workspace_members WHERE workspace_id=? AND role IN ('owner','editor')`, f.workspace).Scan(t.Context(), &allocated))
	require.Equal(t, 3, allocated)
}

// TestExpiredRevokedAndDowngradedInvitationsNeverRestoreAccess checks stale tokens against current state.
func (s *accessSuite) TestExpiredRevokedAndDowngradedInvitationsNeverRestoreAccess() {
	t := s.T()
	for _, condition := range []string{"expired", "revoked", "downgraded", "changed email"} {
		t.Run(condition, func(t *testing.T) {
			f := s.setup(t)
			s.confirmEmail(t, f.outsider, "stale@example.test", true)
			workflow := s.membership(t)
			var invitation workspace.Invitation
			require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
				var err error
				invitation, err = workflow.Invite(ctx, access.Target{WorkspaceID: f.workspace}, workspace.InviteParams{Email: "stale@example.test", Role: workspace.Editor}, uuid.NewString())
				return err
			}))
			expected := workspace.ErrInvitationGone
			switch condition {
			case "expired":
				_, err := s.admin.ExecContext(t.Context(), `UPDATE handdraw.invitations SET created_at=clock_timestamp()-interval '8 days',expires_at=clock_timestamp()-interval '1 day' WHERE id=?`, invitation.ID)
				require.NoError(t, err)
			case "revoked":
				require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error { return workflow.Revoke(ctx, invitation.ID) }))
			case "downgraded":
				_, err := s.admin.ExecContext(t.Context(), `UPDATE handdraw.subscriptions SET status='ended',access_expires_at=clock_timestamp() WHERE workspace_id=?`, f.workspace)
				require.NoError(t, err)
				expected = workspace.ErrForbidden
			case "changed email":
				s.confirmEmail(t, f.outsider, "new-address@example.test", true)
				expected = workspace.ErrForbidden
			}
			err := rlstx.Run(t.Context(), s.request, f.outsider.id, func(ctx context.Context) error {
				_, err := workflow.Accept(ctx, invitationToken(t, invitation.ID))
				return err
			})
			require.ErrorIs(t, err, expected)
			var count int
			require.NoError(t, s.admin.NewRaw(`SELECT count(*) FROM handdraw.workspace_members WHERE workspace_id=? AND user_id=?`, f.workspace, f.outsider.id).Scan(t.Context(), &count))
			require.Zero(t, count)
		})
	}
}

// TestGrantRemovalPreservesNewWorkspaceMembership recalculates remaining permissions instead of forcing access to null.
func (s *accessSuite) TestGrantRemovalPreservesNewWorkspaceMembership() {
	t := s.T()
	f := s.setup(t)
	s.confirmEmail(t, f.outsider, "both@example.test", true)
	workflow := s.membership(t)
	var b string
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		v, err := s.boards().Create(ctx, f.workspace, uuid.NewString(), boardinput.CreateBoard{Name: "Both", Initialization: "empty"})
		b = v.Board.ID()
		return err
	}))
	for _, target := range []access.Target{{BoardID: b}, {WorkspaceID: f.workspace}} {
		var invitation workspace.Invitation
		require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
			var err error
			invitation, err = workflow.Invite(ctx, target, workspace.InviteParams{Email: "both@example.test", Role: workspace.Viewer}, uuid.NewString())
			return err
		}))
		require.NoError(t, rlstx.Run(t.Context(), s.request, f.outsider.id, func(ctx context.Context) error {
			_, err := workflow.Accept(ctx, invitationToken(t, invitation.ID))
			return err
		}))
	}
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		result, err := workflow.RemoveGuest(ctx, b, f.outsider.id)
		require.Equal(t, &membership.RemainingAccess{Role: workspace.Viewer, Source: "workspace"}, result.EffectiveAccess)
		return err
	}))
	decision, err := s.decision(t, f.outsider.id, f.workspace)
	require.NoError(t, err)
	require.True(t, decision.Capabilities.CanRead)
	require.False(t, decision.Capabilities.CanEditContent)
}

// TestInvitationFailureRollsBackReservationAndDeletionCleansSharing checks transactional cleanup across the new tables.
func (s *accessSuite) TestInvitationFailureRollsBackReservationAndDeletionCleansSharing() {
	t := s.T()
	f := s.setup(t)
	workflow := s.membership(t)
	key := uuid.NewString()
	var abandoned string
	err := rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		invitation, err := workflow.Invite(ctx, access.Target{WorkspaceID: f.workspace}, workspace.InviteParams{Email: "rollback@example.test", Role: workspace.Editor}, key)
		if err != nil {
			return err
		}
		abandoned = invitation.ID
		return access.ErrDenied
	})
	require.ErrorIs(t, err, access.ErrDenied)
	var count int
	require.NoError(t, s.admin.NewRaw(`SELECT count(*) FROM handdraw.invitations WHERE id=?`, abandoned).Scan(t.Context(), &count))
	require.Zero(t, count)
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		invitation, err := workflow.Invite(ctx, access.Target{WorkspaceID: f.workspace}, workspace.InviteParams{Email: "rollback@example.test", Role: workspace.Editor}, key)
		require.NotEqual(t, abandoned, invitation.ID)
		return err
	}))
	s.confirmEmail(t, f.outsider, "cleanup@example.test", true)
	var b string
	var invitation workspace.Invitation
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error {
		v, err := s.boards().Create(ctx, f.workspace, uuid.NewString(), boardinput.CreateBoard{Name: "Cleanup grants", Initialization: "empty"})
		if err != nil {
			return err
		}
		b = v.Board.ID()
		invitation, err = workflow.Invite(ctx, access.Target{BoardID: b}, workspace.InviteParams{Email: "cleanup@example.test", Role: workspace.Viewer}, uuid.NewString())
		return err
	}))
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.outsider.id, func(ctx context.Context) error {
		_, err := workflow.Accept(ctx, invitationToken(t, invitation.ID))
		return err
	}))
	require.NoError(t, rlstx.Run(t.Context(), s.request, f.owner.id, func(ctx context.Context) error { _, err := s.boards().Delete(ctx, b, 1); return err }))
	worker := jobdb.NewWorker(s.cleanup)
	n, err := worker.RunOne(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	for _, table := range []string{"board_grants", "invitations"} {
		require.NoError(t, s.admin.NewRaw(`SELECT count(*) FROM handdraw.`+table+` WHERE board_id=?`, b).Scan(t.Context(), &count))
		require.Zero(t, count)
	}
}
