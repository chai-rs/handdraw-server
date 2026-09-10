// Package model defines billing intentions and the verified provider boundary.
package model

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	billing "github.com/chai-rs/handdraw-server/internal/billing/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

const (
	// IDPrefix identifies a durable billing intention, including quotes.
	IDPrefix = "bci"
	// WorkspaceIDPrefix identifies the unchanged billing scope.
	WorkspaceIDPrefix = "ws"
	// EventIDPrefix identifies an accepted provider delivery.
	EventIDPrefix = "evt"
)

var (
	// ErrInvalid rejects malformed billing input.
	ErrInvalid = errors.New("invalid billing request")
	// ErrConflict rejects stale confirmations or unsupported provider capabilities.
	ErrConflict = errors.New("billing conflict")
	// ErrDenied rejects non-Owner billing access.
	ErrDenied = errors.New("billing permission denied")
	// ErrNotFound hides inaccessible billing state.
	ErrNotFound = errors.New("billing not found")
	// ErrEmpty means there is no due billing work.
	ErrEmpty = errors.New("no billing work")
)

// Command contains client choices only; amounts and removal sets come from the server.
type Command struct {
	CancelAtPeriodEnd    *bool    `json:"cancel_at_period_end,omitempty"`
	Plan                 string   `json:"plan,omitempty"`
	TargetPlan           string   `json:"target_plan,omitempty"`
	BillingInterval      string   `json:"billing_interval,omitempty"`
	EditorSeats          int      `json:"editor_seats,omitempty"`
	QuoteID              string   `json:"quote_id,omitempty"`
	PlanChangeID         string   `json:"plan_change_id,omitempty"`
	IntentRevision       string   `json:"intent_revision,omitempty"`
	ConfirmMemberChanges bool     `json:"confirm_member_changes,omitempty"`
	DemoteUserIDs        []string `json:"demote_user_ids,omitempty"`
	RevokeInvitationIDs  []string `json:"revoke_invitation_ids,omitempty"`
	ReturnPath           string   `json:"return_path,omitempty"`
}

// Validate checks request shape before entering the authorized repository operation.
func (p Command) Validate(action string) error {
	if p.CancelAtPeriodEnd != nil && (action != "cancel" || !*p.CancelAtPeriodEnd) {
		return ErrInvalid
	}

	if p.IntentRevision != "" {
		rev, err := strconv.ParseInt(p.IntentRevision, 10, 64)
		if err != nil || rev < 1 || strconv.FormatInt(rev, 10) != p.IntentRevision {
			return ErrInvalid
		}
	}

	if len(p.DemoteUserIDs) > 100 || len(p.RevokeInvitationIDs) > 100 {
		return ErrInvalid
	}

	for _, id := range p.DemoteUserIDs {
		if resourceid.Validate(id, "usr") != nil {
			return ErrInvalid
		}
	}

	for _, id := range p.RevokeInvitationIDs {
		if resourceid.Validate(id, "inv") != nil {
			return ErrInvalid
		}
	}

	if valx.Var(action, valx.In("subscription", "history", "status", "quote", "confirm", "checkout", "cancel", "resume")) != nil {
		return ErrInvalid
	}

	if p.Plan != "" && valx.Var(p.Plan, valx.In("cloud", "team")) != nil {
		return ErrInvalid
	}

	if p.TargetPlan != "" && valx.Var(p.TargetPlan, valx.In("cloud", "team")) != nil {
		return ErrInvalid
	}

	if p.BillingInterval != "" && valx.Var(p.BillingInterval, valx.In("month", "year")) != nil {
		return ErrInvalid
	}

	if p.EditorSeats < 0 || p.EditorSeats > 100 || p.ReturnPath != "" && p.ReturnPath != "/app/cloud" {
		return ErrInvalid
	}

	for _, id := range []string{p.QuoteID, p.PlanChangeID} {
		if id != "" && resourceid.Validate(id, IDPrefix) != nil {
			return ErrInvalid
		}
	}

	if p.Plan != "" && p.TargetPlan != "" || p.PlanChangeID != "" && p.Plan != "" {
		return ErrInvalid
	}

	if action == "confirm" && (p.QuoteID == "" || p.IntentRevision == "") {
		return ErrInvalid
	}

	return nil
}

// Task carries a short worker lease, never a request credential.
type Task struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

// Snapshot is a provider-verified billing observation. Requests cannot supply it.
type Snapshot struct {
	Version             int64      `json:"version"`
	SubscriptionID      string     `json:"subscription_id"`
	Status              string     `json:"status"`
	CardVerified        bool       `json:"card_verified"`
	TrialEndsAt         *time.Time `json:"trial_ends_at,omitempty"`
	PaidThroughAt       *time.Time `json:"paid_through_at,omitempty"`
	RenewalFailureAt    *time.Time `json:"renewal_failure_at,omitempty"`
	RenewalObligationID string     `json:"renewal_obligation_id,omitempty"`
	CancelAtPeriodEnd   bool       `json:"cancel_at_period_end"`
	Orders              []Order    `json:"orders"`
	Refunds             []Refund   `json:"refunds"`
}

// Order records paid money and its covered service interval, independent of subscription status.
type Order struct {
	ID              string    `json:"id"`
	ProviderOrderID string    `json:"provider_order_id"`
	AmountMinor     int64     `json:"amount_minor"`
	PaidAt          time.Time `json:"paid_at"`
	ServiceFrom     time.Time `json:"service_from"`
	ServiceUntil    time.Time `json:"service_until"`
}

// Refund references the original provider order and records an immutable partial or full refund.
type Refund struct {
	ID               string    `json:"id"`
	ProviderOrderID  string    `json:"provider_order_id"`
	ProviderRefundID string    `json:"provider_refund_id"`
	AmountMinor      int64     `json:"amount_minor"`
	RefundedAt       time.Time `json:"refunded_at"`
}

// Validate bounds the fixture control surface and requires canonical financial identifiers.
func (p Snapshot) Validate() error {
	if p.Version < 1 || len(p.SubscriptionID) > 200 || valx.Var(p.Status, valx.In("pending", "trialing", "active", "renewal_failed", "ended", "failed")) != nil || len(p.Orders) > 100 || len(p.Refunds) > 100 {
		return ErrInvalid
	}

	for _, o := range p.Orders {
		if resourceid.Validate(o.ID, billing.OrderIDPrefix) != nil || o.AmountMinor < 1 || o.AmountMinor > 1_000_000_000 || o.ProviderOrderID == "" || len(o.ProviderOrderID) > 200 || !o.ServiceUntil.After(o.ServiceFrom) || o.PaidAt.IsZero() {
			return ErrInvalid
		}
	}

	for _, r := range p.Refunds {
		if resourceid.Validate(r.ID, billing.RefundIDPrefix) != nil || r.AmountMinor < 1 || r.ProviderOrderID == "" || r.ProviderRefundID == "" || len(r.ProviderRefundID) > 200 || r.RefundedAt.IsZero() {
			return ErrInvalid
		}
	}

	return nil
}

// Repository persists request intentions and executes narrow worker operations.
//
//mockery:generate: true
type Repository interface {
	Request(context.Context, string, string, string, string, Command) (json.RawMessage, error)
	Lease(context.Context, string) (Task, error)
	Apply(context.Context, Task, Snapshot) error
	Maintain(context.Context) error
}

// Provider executes or resumes the same external operation outside the application transaction.
//
//mockery:generate: true
type Provider interface {
	Observe(context.Context, string) (Snapshot, error)
}
