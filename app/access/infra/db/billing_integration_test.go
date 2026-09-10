//go:build integration

package db_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	assetdb "github.com/chai-rs/handdraw-server/app/asset_management/infra/db"
	billingdb "github.com/chai-rs/handdraw-server/app/billing/infra/db"
	"github.com/chai-rs/handdraw-server/app/billing/infra/local"
	"github.com/chai-rs/handdraw-server/app/billing/model"
	billingservice "github.com/chai-rs/handdraw-server/app/billing/service"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func (s *accessSuite) billingRequest(t *testing.T, actor, w, action string, p model.Command) (map[string]any, error) {
	t.Helper()
	var result json.RawMessage
	err := rlstx.Run(t.Context(), s.request, actor, func(ctx context.Context) error {
		var e error
		result, e = billingservice.New(billingdb.New(nil), nil).Request(ctx, w, action, uuid.NewString(), p)
		return e
	})
	var data map[string]any
	if err == nil {
		require.NoError(t, json.Unmarshal(result, &data))
	}
	return data, err
}

func (s *accessSuite) billingQuote(t *testing.T, owner, w, plan string, seats int) map[string]any {
	t.Helper()
	q, err := s.billingRequest(t, owner, w, "quote", model.Command{TargetPlan: plan, BillingInterval: "month", EditorSeats: seats})
	require.NoError(t, err)
	return q
}

func (s *accessSuite) billingConfirm(t *testing.T, owner, w string, q map[string]any) map[string]any {
	t.Helper()
	q, err := s.billingRequest(t, owner, w, "confirm", model.Command{QuoteID: q["id"].(string), IntentRevision: q["intent_revision"].(string), ConfirmMemberChanges: true})
	require.NoError(t, err)
	return q
}

func (s *accessSuite) billingLease(t *testing.T, id string) model.Task {
	t.Helper()
	_, err := s.admin.ExecContext(t.Context(), "UPDATE handdraw.billing_change_intents SET retry_at=clock_timestamp()+interval '1 day',lease_token=NULL,lease_until=NULL")
	require.NoError(t, err)
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.billing_change_intents SET retry_at=clock_timestamp() WHERE id=?", id)
	require.NoError(t, err)
	task, err := billingdb.New(s.billing).Lease(t.Context(), strings.Repeat("a", 64))
	require.NoError(t, err)
	require.Equal(t, id, task.ID)
	return task
}

func (s *accessSuite) billingObserve(t *testing.T, id string, state model.Snapshot) error {
	t.Helper()
	p := local.New(s.billing)
	_, err := p.Observe(t.Context(), id)
	require.NoError(t, err)
	require.NoError(t, p.Deliver(t.Context(), id, fmt.Sprintf("%s-%d", id, state.Version), state))
	canonical, err := p.Observe(t.Context(), id)
	require.NoError(t, err)
	return billingdb.New(s.billing).Apply(t.Context(), s.billingLease(t, id), canonical)
}

func payment(t *testing.T, id string, amount int64) model.Snapshot {
	t.Helper()
	now := time.Now().UTC().Add(-time.Minute)
	until := now.AddDate(0, 1, 0)
	order, err := resourceid.New("ord")
	require.NoError(t, err)
	return model.Snapshot{Version: 2, SubscriptionID: "local_" + id, Status: "active", CardVerified: true, PaidThroughAt: &until, Orders: []model.Order{{ID: order, ProviderOrderID: "receipt-" + order, AmountMinor: amount, PaidAt: now, ServiceFrom: now, ServiceUntil: until}}, Refunds: []model.Refund{}}
}

// TestBillingCardFirstTrialAndHTTPBoundary proves checkout admission alone cannot grant content access.
func (s *accessSuite) TestBillingCardFirstTrialAndHTTPBoundary() {
	t := s.T()
	owner := s.user(t)
	w := s.seedWorkspace(t, owner, "personal")
	_, err := s.admin.ExecContext(t.Context(), "DELETE FROM handdraw.subscriptions WHERE workspace_id=?", w)
	require.NoError(t, err)
	base, token := s.http(t)
	status, body, _ := request(t, http.MethodPost, base+"/v1/workspaces/"+w+"/billing/checkouts", token(owner), `{"plan":"cloud","billing_interval":"month","editor_seats":1,"return_path":"/app/cloud"}`, "", uuid.NewString())
	require.Equal(t, 202, status)
	q := body["result"].(map[string]any)
	id := q["id"].(string)
	d, err := s.decision(t, owner.id, w)
	require.NoError(t, err)
	require.False(t, d.Capabilities.CanEditContent)
	ends := time.Now().UTC().Add(7*24*time.Hour - time.Minute)
	state := model.Snapshot{Version: 2, SubscriptionID: "local_" + id, Status: "trialing", TrialEndsAt: &ends, Orders: []model.Order{}, Refunds: []model.Refund{}}
	require.ErrorIs(t, s.billingObserve(t, id, state), model.ErrConflict)
	state.Version++
	state.CardVerified = true
	require.NoError(t, s.billingObserve(t, id, state))
	d, err = s.decision(t, owner.id, w)
	require.NoError(t, err)
	require.True(t, d.Capabilities.CanEditContent)
	var stored time.Time
	require.NoError(t, s.admin.NewRaw("SELECT trial_ends_at FROM handdraw.subscriptions WHERE workspace_id=?", w).Scan(t.Context(), &stored))
	require.WithinDuration(t, ends, stored, time.Microsecond)
	// Upgrade carries the original trial deadline and cannot create a sixth trial seat.
	_, err = s.billingRequest(t, owner.id, w, "quote", model.Command{TargetPlan: "team", BillingInterval: "month", EditorSeats: 6})
	require.ErrorIs(t, err, model.ErrConflict)
	upgrade := s.billingConfirm(t, owner.id, w, s.billingQuote(t, owner.id, w, "team", 3))
	target := upgrade["id"].(string)
	state.SubscriptionID = "local_" + target
	state.Version = 2
	require.NoError(t, s.billingObserve(t, target, state))
	var kind string
	require.NoError(t, s.admin.NewRaw("SELECT kind FROM handdraw.workspaces WHERE id=?", w).Scan(t.Context(), &kind))
	require.Equal(t, "team", kind)
	require.NoError(t, s.admin.NewRaw("SELECT trial_ends_at FROM handdraw.subscriptions WHERE workspace_id=?", w).Scan(t.Context(), &stored))
	require.WithinDuration(t, ends, stored, time.Microsecond)
	outsider := s.user(t)
	status, _, _ = request(t, http.MethodGet, base+"/v1/workspaces/"+w+"/billing/history", token(outsider), "", "")
	require.Equal(t, 404, status)
	require.NoError(t, billingdb.New(s.billing).Check(t.Context()))
	require.Error(t, billingdb.New(s.request).Check(t.Context()))
	for _, sql := range []string{"SELECT handdraw.local_billing_prepare('x')", "UPDATE handdraw.subscriptions SET status='active'", "SELECT * FROM handdraw.payment_orders"} {
		_, err = s.request.ExecContext(t.Context(), sql)
		require.Error(t, err)
	}
}

// TestBillingPaymentHistoryRecoveryAndRenewalGrace covers commit recovery, duplicate delivery and first-failure anchoring.
func (s *accessSuite) TestBillingPaymentHistoryRecoveryAndRenewalGrace() {
	t := s.T()
	owner := s.user(t)
	w := s.seedWorkspace(t, owner, "personal")
	_, err := s.admin.ExecContext(t.Context(), "DELETE FROM handdraw.subscriptions WHERE workspace_id=?", w)
	require.NoError(t, err)
	q, err := s.billingRequest(t, owner.id, w, "checkout", model.Command{Plan: "cloud", BillingInterval: "month", EditorSeats: 1})
	require.NoError(t, err)
	id := q["id"].(string)
	state := payment(t, id, 800)
	p := local.New(s.billing)
	_, err = p.Observe(t.Context(), id)
	require.NoError(t, err)
	require.NoError(t, p.Deliver(t.Context(), id, "first-"+id, state))
	// Provider success commits first. A failed application transaction leaves the exact operation recoverable.
	task := s.billingLease(t, id)
	stale := task
	stale.Token = strings.Repeat("b", 64)
	require.ErrorIs(t, billingdb.New(s.billing).Apply(t.Context(), stale, state), model.ErrConflict)
	p = local.New(s.billing)
	observed, err := p.Observe(t.Context(), id)
	require.NoError(t, err)
	require.NoError(t, billingdb.New(s.billing).Apply(t.Context(), task, observed))
	require.NoError(t, p.Deliver(t.Context(), id, "first-"+id, state))
	require.NoError(t, s.billingObserve(t, id, state))
	refund, err := resourceid.New("rfnd")
	require.NoError(t, err)
	state.Version = 3
	state.Refunds = []model.Refund{{ID: refund, ProviderOrderID: state.Orders[0].ProviderOrderID, ProviderRefundID: "refund-" + refund, AmountMinor: 200, RefundedAt: time.Now().UTC()}}
	require.NoError(t, s.billingObserve(t, id, state))
	history, err := s.billingRequest(t, owner.id, w, "history", model.Command{})
	require.NoError(t, err)
	require.Len(t, history["orders"], 1)
	require.Len(t, history["refunds"], 1)
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET paid_through_at=clock_timestamp()-interval '2 hours' WHERE workspace_id=?", w)
	require.NoError(t, err)
	failure := time.Now().UTC().Add(-time.Hour)
	state.Version = 4
	state.Status = "renewal_failed"
	state.RenewalFailureAt = &failure
	state.RenewalObligationID = "renewal-1"
	require.NoError(t, s.billingObserve(t, id, state))
	var grace time.Time
	require.NoError(t, s.admin.NewRaw("SELECT grace_ends_at FROM handdraw.subscriptions WHERE workspace_id=?", w).Scan(t.Context(), &grace))
	require.WithinDuration(t, failure.Add(7*24*time.Hour), grace, time.Microsecond)
	later := time.Now().UTC()
	state.Version = 5
	state.RenewalFailureAt = &later
	state.RenewalObligationID = "retry-new-invoice"
	require.NoError(t, s.billingObserve(t, id, state))
	var again time.Time
	require.NoError(t, s.admin.NewRaw("SELECT grace_ends_at FROM handdraw.subscriptions WHERE workspace_id=?", w).Scan(t.Context(), &again))
	require.True(t, grace.Equal(again))
	state.Version = 2
	state.Status = "ended"
	require.NoError(t, p.Deliver(t.Context(), id, "old-reordered-"+id, state))
	canonical, err := p.Observe(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, int64(5), canonical.Version)
}

// TestBillingDowngradeRequiresFreshExactConsent keeps IDs and unrelated board Guests intact.
func (s *accessSuite) TestBillingDowngradeRequiresFreshExactConsent() {
	t := s.T()
	f := s.setup(t)
	_ = s.seedWorkspace(t, f.owner, "personal")
	_, err := s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='active',provider_subscription_id=?,trial_ends_at=NULL,paid_through_at=clock_timestamp()+interval '1 day',access_expires_at=clock_timestamp()+interval '1 day',last_successful_payment_at=clock_timestamp() WHERE workspace_id=?", "source-"+f.workspace, f.workspace)
	require.NoError(t, err)
	base, token := s.http(t)
	board := s.discussionBoard(t, f, base, token(f.owner))
	_, err = s.admin.ExecContext(t.Context(), "INSERT INTO handdraw.board_grants(workspace_id,board_id,user_id,granted_by) VALUES (?,?,?,?),(?,?,?,?)", f.workspace, board, f.editor.id, f.owner.id, f.workspace, board, f.outsider.id, f.owner.id)
	require.NoError(t, err)
	var original []byte
	require.NoError(t, s.admin.NewRaw("SELECT state FROM handdraw.board_documents WHERE board_id=?", board).Scan(t.Context(), &original))
	q := s.billingConfirm(t, f.owner.id, f.workspace, s.billingQuote(t, f.owner.id, f.workspace, "cloud", 1))
	id := q["id"].(string)
	_, err = s.billingRequest(t, f.owner.id, f.workspace, "checkout", model.Command{PlanChangeID: id, IntentRevision: q["intent_revision"].(string), ConfirmMemberChanges: true})
	require.ErrorIs(t, err, model.ErrConflict)
	// Verify source end, then refresh the original intention without replacing its payment identity.
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='ended',paid_through_at=clock_timestamp()-interval '1 minute',access_expires_at=clock_timestamp()-interval '1 minute',revision=revision+1 WHERE workspace_id=?", f.workspace)
	require.NoError(t, err)
	q, err = s.billingRequest(t, f.owner.id, f.workspace, "quote", model.Command{PlanChangeID: id, TargetPlan: "cloud", BillingInterval: "month", EditorSeats: 1})
	require.NoError(t, err)
	q = s.billingConfirm(t, f.owner.id, f.workspace, q)
	_, err = s.billingRequest(t, f.owner.id, f.workspace, "checkout", model.Command{PlanChangeID: id, IntentRevision: q["intent_revision"].(string), ConfirmMemberChanges: true})
	require.NoError(t, err)
	state := payment(t, id, 800)
	// A safety removal while checkout is pending invalidates the snapshot; payment alone cannot remove the remaining member.
	_, err = s.admin.ExecContext(t.Context(), "DELETE FROM handdraw.workspace_members WHERE workspace_id=? AND user_id=?", f.workspace, f.viewer.id)
	require.NoError(t, err)
	require.NoError(t, s.billingObserve(t, id, state))
	var kind string
	require.NoError(t, s.admin.NewRaw("SELECT kind FROM handdraw.workspaces WHERE id=?", f.workspace).Scan(t.Context(), &kind))
	require.Equal(t, "team", kind)
	q, err = s.billingRequest(t, f.owner.id, f.workspace, "quote", model.Command{PlanChangeID: id, TargetPlan: "cloud", BillingInterval: "month", EditorSeats: 1})
	require.NoError(t, err)
	q = s.billingConfirm(t, f.owner.id, f.workspace, q)
	require.NoError(t, s.billingObserve(t, id, state))
	require.NoError(t, s.admin.NewRaw("SELECT kind FROM handdraw.workspaces WHERE id=?", f.workspace).Scan(t.Context(), &kind))
	require.Equal(t, "personal", kind)
	var members int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.workspace_members WHERE workspace_id=?", f.workspace).Scan(t.Context(), &members))
	require.Equal(t, 1, members)
	var grants []string
	require.NoError(t, s.admin.NewRaw("SELECT user_id FROM handdraw.board_grants WHERE board_id=?", board).Scan(t.Context(), &grants))
	require.Equal(t, []string{f.outsider.id}, grants)
	var after []byte
	require.NoError(t, s.admin.NewRaw("SELECT state FROM handdraw.board_documents WHERE board_id=?", board).Scan(t.Context(), &after))
	require.Equal(t, original, after)

	d, err := s.decision(t, f.owner.id, f.workspace)
	require.NoError(t, err)
	require.True(t, d.Capabilities.CanEditContent)
	var orders int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.payment_orders WHERE workspace_id=?", f.workspace).Scan(t.Context(), &orders))
	require.Equal(t, 1, orders)
}

// TestBillingRetentionNoticesAndPurgeBarrier proves maintenance cannot resurrect an expired workspace.
func (s *accessSuite) TestBillingRetentionNoticesAndPurgeBarrier() {
	t := s.T()
	owner := s.user(t)
	w := s.seedWorkspace(t, owner, "personal")
	_, err := s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='ended',trial_ends_at=NULL,access_expires_at=clock_timestamp()-interval '89 days 1 hour' WHERE workspace_id=?", w)
	require.NoError(t, err)
	repo := billingdb.New(s.billing)
	require.NoError(t, repo.Maintain(t.Context()))
	require.NoError(t, repo.Maintain(t.Context()))
	var notices int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.billing_notice_outbox WHERE workspace_id=?", w).Scan(t.Context(), &notices))
	require.Equal(t, 4, notices)
	// Prepare a paid reactivation, but let the purge barrier win before the payment observation is applied.
	q, err := s.billingRequest(t, owner.id, w, "checkout", model.Command{Plan: "cloud", BillingInterval: "month", EditorSeats: 1})
	require.NoError(t, err)
	id := q["id"].(string)
	state := payment(t, id, 800)
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET access_expires_at=clock_timestamp()-interval '91 days' WHERE workspace_id=?", w)
	require.NoError(t, err)
	require.NoError(t, repo.Maintain(t.Context()))
	require.NoError(t, s.billingObserve(t, id, state))
	var lifecycle string
	require.NoError(t, s.admin.NewRaw("SELECT lifecycle FROM handdraw.workspaces WHERE id=?", w).Scan(t.Context(), &lifecycle))
	require.Equal(t, "purging", lifecycle)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { errs <- repo.Maintain(t.Context()) })
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	require.NoError(t, s.admin.NewRaw("SELECT lifecycle FROM handdraw.workspaces WHERE id=?", w).Scan(t.Context(), &lifecycle))
	require.Equal(t, "deleted", lifecycle)
	require.NoError(t, s.billingObserve(t, id, state))
	require.NoError(t, s.admin.NewRaw("SELECT lifecycle FROM handdraw.workspaces WHERE id=?", w).Scan(t.Context(), &lifecycle))
	require.Equal(t, "deleted", lifecycle)
}

// TestBillingPaidUpgradeAndScheduledSeats preserves paid Cloud rights until the target invoice is verified.
func (s *accessSuite) TestBillingPaidUpgradeAndScheduledSeats() {
	t := s.T()
	owner := s.user(t)
	w := s.seedWorkspace(t, owner, "personal")
	_, err := s.admin.ExecContext(t.Context(), "DELETE FROM handdraw.subscriptions WHERE workspace_id=?", w)
	require.NoError(t, err)
	q, err := s.billingRequest(t, owner.id, w, "checkout", model.Command{Plan: "cloud", BillingInterval: "month", EditorSeats: 1})
	require.NoError(t, err)
	source := q["id"].(string)
	paid := payment(t, source, 800)
	require.NoError(t, s.billingObserve(t, source, paid))
	q = s.billingConfirm(t, owner.id, w, s.billingQuote(t, owner.id, w, "team", 3))
	target := q["id"].(string)
	pending := model.Snapshot{Version: 2, SubscriptionID: "local_" + target, Status: "failed", Orders: []model.Order{}, Refunds: []model.Refund{}}
	require.NoError(t, s.billingObserve(t, target, pending))
	d, err := s.decision(t, owner.id, w)
	require.NoError(t, err)
	require.True(t, d.Capabilities.CanEditContent)
	var kind string
	require.NoError(t, s.admin.NewRaw("SELECT kind FROM handdraw.workspaces WHERE id=?", w).Scan(t.Context(), &kind))
	require.Equal(t, "personal", kind)
	upgrade := payment(t, target, 3600)
	upgrade.Version = 3
	upgrade.PaidThroughAt = paid.PaidThroughAt
	upgrade.Orders[0].ServiceUntil = *paid.PaidThroughAt
	require.NoError(t, s.billingObserve(t, target, upgrade))
	editor := s.user(t)
	_, err = s.admin.ExecContext(t.Context(), "INSERT INTO handdraw.workspace_members(workspace_id,user_id,role) VALUES (?,?,'editor')", w, editor.id)
	require.NoError(t, err)
	q = s.billingQuote(t, owner.id, w, "team", 1)
	_, err = s.billingRequest(t, owner.id, w, "confirm", model.Command{QuoteID: q["id"].(string), IntentRevision: q["intent_revision"].(string)})
	require.ErrorIs(t, err, model.ErrConflict)
	q, err = s.billingRequest(t, owner.id, w, "confirm", model.Command{QuoteID: q["id"].(string), IntentRevision: q["intent_revision"].(string), DemoteUserIDs: []string{editor.id}})
	require.NoError(t, err)
	lower := q["id"].(string)
	next := payment(t, lower, 1200)
	require.NoError(t, s.billingObserve(t, lower, model.Snapshot{Version: 2, SubscriptionID: "local_" + lower, Status: "pending", Orders: []model.Order{}, Refunds: []model.Refund{}}))
	var seats int
	require.NoError(t, s.admin.NewRaw("SELECT paid_seats FROM handdraw.subscriptions WHERE workspace_id=?", w).Scan(t.Context(), &seats))
	require.Equal(t, 3, seats)
	// Advance the fixture to period end; maintenance changes only billing revisions, not the reviewed access set.
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET paid_through_at=clock_timestamp()-interval '1 second',access_expires_at=clock_timestamp()-interval '1 second' WHERE workspace_id=?", w)
	require.NoError(t, err)
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.billing_change_intents SET effective_at=clock_timestamp()-interval '1 second',source_period_end=(SELECT paid_through_at FROM handdraw.subscriptions WHERE workspace_id=?) WHERE id=?", w, lower)
	require.NoError(t, err)
	require.NoError(t, billingdb.New(s.billing).Maintain(t.Context()))
	next.Version = 3
	require.NoError(t, s.billingObserve(t, lower, next))
	require.NoError(t, s.admin.NewRaw("SELECT paid_seats FROM handdraw.subscriptions WHERE workspace_id=?", w).Scan(t.Context(), &seats))
	require.Equal(t, 1, seats)
	d, err = s.decision(t, editor.id, w)
	require.NoError(t, err)
	require.False(t, d.Capabilities.CanEditContent)
	require.True(t, d.Capabilities.CanRead)
	// Late source payment failure cannot overwrite the current Team contract or start retention again.
	paid.Version = 9
	paid.Status = "ended"
	require.NoError(t, s.billingObserve(t, source, paid))
	d, err = s.decision(t, owner.id, w)
	require.NoError(t, err)
	require.True(t, d.Capabilities.CanEditContent)
}

// TestBillingPurgeWaitsForGarageAbsence keeps quota until private bytes are removed and preserves financial tombstones.
func (s *accessSuite) TestBillingPurgeWaitsForGarageAbsence() {
	t := s.T()
	f := s.setup(t)
	base, token := s.http(t)
	board := s.discussionBoard(t, f, base, token(f.owner))
	data := tinyPNG(t)
	assetID := s.upload(t, base, token(f.owner), board, "attachment", "image/png", data)
	_, err := s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='ended',trial_ends_at=NULL,access_expires_at=clock_timestamp()-interval '91 days' WHERE workspace_id=?", f.workspace)
	require.NoError(t, err)
	repo := billingdb.New(s.billing)
	require.NoError(t, repo.Maintain(t.Context()))
	require.NoError(t, repo.Maintain(t.Context()))
	var used int64
	require.NoError(t, s.admin.NewRaw("SELECT used_bytes FROM handdraw.workspace_usage WHERE workspace_id=?", f.workspace).Scan(t.Context(), &used))
	require.Equal(t, int64(len(data)), used)
	var state string
	require.NoError(t, s.admin.NewRaw("SELECT status FROM handdraw.assets WHERE id=?", assetID).Scan(t.Context(), &state))
	require.Equal(t, "deleting", state)
	worker := assetdb.NewWorker(s.cleanup, s.assets)
	for range 30 {
		_, err = worker.RunOne(t.Context())
		require.NoError(t, err)
	}
	require.NoError(t, s.admin.NewRaw("SELECT used_bytes FROM handdraw.workspace_usage WHERE workspace_id=?", f.workspace).Scan(t.Context(), &used))
	require.Zero(t, used)
	require.NoError(t, repo.Maintain(t.Context()))
	require.NoError(t, repo.Maintain(t.Context()))
	var count int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.board_documents WHERE workspace_id=?", f.workspace).Scan(t.Context(), &count))
	require.Zero(t, count)
	require.NoError(t, s.admin.NewRaw("SELECT lifecycle FROM handdraw.workspaces WHERE id=?", f.workspace).Scan(t.Context(), &state))
	require.Equal(t, "deleted", state)
}

// TestBillingConcurrentCheckoutAndExpiredQuote permits only one durable operation and no stale confirmation.
func (s *accessSuite) TestBillingConcurrentCheckoutAndExpiredQuote() {
	t := s.T()
	owner := s.user(t)
	w := s.seedWorkspace(t, owner, "personal")
	_, err := s.admin.ExecContext(t.Context(), "DELETE FROM handdraw.subscriptions WHERE workspace_id=?", w)
	require.NoError(t, err)
	key := uuid.NewString()
	svc := billingservice.New(billingdb.New(nil), nil)
	results := make(chan json.RawMessage, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			var data json.RawMessage
			e := rlstx.Run(t.Context(), s.request, owner.id, func(ctx context.Context) error {
				var e error
				data, e = svc.Request(ctx, w, "checkout", key, model.Command{Plan: "cloud", BillingInterval: "month", EditorSeats: 1})
				return e
			})
			results <- data
			errs <- e
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	one, two := <-results, <-results
	require.JSONEq(t, string(one), string(two))
	var q map[string]any
	require.NoError(t, json.Unmarshal(one, &q))
	id := q["id"].(string)
	require.NoError(t, s.billingObserve(t, id, payment(t, id, 800)))
	q = s.billingQuote(t, owner.id, w, "team", 2)
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.billing_change_intents SET expires_at=clock_timestamp()-interval '1 second' WHERE id=?", q["id"])
	require.NoError(t, err)
	_, err = s.billingRequest(t, owner.id, w, "confirm", model.Command{QuoteID: q["id"].(string), IntentRevision: q["intent_revision"].(string)})
	require.ErrorIs(t, err, model.ErrConflict)
}

// TestBillingCancellationAndReactivationKeepPaidAccess exercises both sides of the purge barrier.
func (s *accessSuite) TestBillingCancellationAndReactivationKeepPaidAccess() {
	t := s.T()
	owner := s.user(t)
	w := s.seedWorkspace(t, owner, "personal")
	_, err := s.admin.ExecContext(t.Context(), "DELETE FROM handdraw.subscriptions WHERE workspace_id=?", w)
	require.NoError(t, err)
	q, err := s.billingRequest(t, owner.id, w, "checkout", model.Command{Plan: "cloud", BillingInterval: "month", EditorSeats: 1})
	require.NoError(t, err)
	source := q["id"].(string)
	require.NoError(t, s.billingObserve(t, source, payment(t, source, 800)))
	for _, action := range []string{"cancel", "resume"} {
		q, err = s.billingRequest(t, owner.id, w, action, model.Command{})
		require.NoError(t, err)
		id := q["id"].(string)
		provider := local.New(s.billing)
		p, e := provider.Observe(t.Context(), id)
		require.NoError(t, e)
		require.NoError(t, billingdb.New(s.billing).Apply(t.Context(), s.billingLease(t, id), p))
		var canceled bool
		require.NoError(t, s.admin.NewRaw("SELECT cancel_at_period_end FROM handdraw.subscriptions WHERE workspace_id=?", w).Scan(t.Context(), &canceled))
		require.Equal(t, action == "cancel", canceled)
		d, e := s.decision(t, owner.id, w)
		require.NoError(t, e)
		require.True(t, d.Capabilities.CanEditContent)
	}
	_, err = s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='ended',paid_through_at=clock_timestamp()-interval '89 days',access_expires_at=clock_timestamp()-interval '89 days' WHERE workspace_id=?", w)
	require.NoError(t, err)
	repo := billingdb.New(s.billing)
	require.NoError(t, repo.Maintain(t.Context()))
	q, err = s.billingRequest(t, owner.id, w, "checkout", model.Command{Plan: "cloud", BillingInterval: "month", EditorSeats: 1})
	require.NoError(t, err)
	target := q["id"].(string)
	require.NoError(t, s.billingObserve(t, target, payment(t, target, 800)))
	require.NoError(t, repo.Maintain(t.Context()))
	var retaining int
	require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.retention_episodes WHERE workspace_id=? AND status='retaining'", w).Scan(t.Context(), &retaining))
	require.Zero(t, retaining)
	var noticeCount int
	require.NoError(t, s.billing.NewRaw("SELECT count(*) FROM handdraw.local_billing_notices() WHERE workspace_id=?", w).Scan(t.Context(), &noticeCount))
	require.Zero(t, noticeCount)
	d, err := s.decision(t, owner.id, w)
	require.NoError(t, err)
	require.True(t, d.Capabilities.CanEditContent)
}

// TestBillingZMaintenanceFairness visits later workspaces even when earlier retaining workspaces remain present.
func (s *accessSuite) TestBillingZMaintenanceFairness() {
	t := s.T()
	owner := s.user(t)
	scopes := make([]string, 25)
	for i := range scopes {
		w := s.seedWorkspace(t, owner, "personal")
		scopes[i] = w
		_, err := s.admin.ExecContext(t.Context(), "UPDATE handdraw.subscriptions SET status='ended',trial_ends_at=NULL,access_expires_at=clock_timestamp()-interval '1 day' WHERE workspace_id=?", w)
		require.NoError(t, err)
	}
	repo := billingdb.New(s.billing)
	for range 4 {
		require.NoError(t, repo.Maintain(t.Context()))
	}
	for _, w := range scopes {
		var count int
		require.NoError(t, s.admin.NewRaw("SELECT count(*) FROM handdraw.billing_notice_outbox WHERE workspace_id=? AND milestone='started'", w).Scan(t.Context(), &count))
		require.Equal(t, 1, count)
	}
}
