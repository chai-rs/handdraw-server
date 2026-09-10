// Package local provides a durable deterministic billing fixture, with no checkout or money movement.
package local

import (
	"context"
	"encoding/json"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/uptrace/bun"
)

// Provider uses the isolated billing credential; it is not reachable through a public settlement endpoint.
type Provider struct{ db *bun.DB }

var _ model.Provider = (*Provider)(nil)

// New creates an explicitly local adapter that resumes operations by the same intent ID after restart.
func New(db *bun.DB) *Provider { return &Provider{db: db} }

// Observe creates an idempotent pending operation or retrieves its authoritative state.
func (p *Provider) Observe(ctx context.Context, id string) (model.Snapshot, error) {
	var raw []byte

	err := p.db.NewRaw("SELECT handdraw.local_billing_prepare(?)", id).Scan(ctx, &raw)
	if err != nil {
		return model.Snapshot{}, err
	}

	var state model.Snapshot

	err = json.Unmarshal(raw, &state)

	return state, err
}

// Deliver is fixture control for verified payment, failure and refund scenarios; application routes never expose it.
func (p *Provider) Deliver(ctx context.Context, id, event string, state model.Snapshot) error {
	if state.Validate() != nil || resourceid.Validate(id, model.IDPrefix) != nil || event == "" || len(event) > 200 {
		return model.ErrInvalid
	}

	if state.Orders == nil {
		state.Orders = []model.Order{}
	}

	if state.Refunds == nil {
		state.Refunds = []model.Refund{}
	}

	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}

	eventID, err := resourceid.New(model.EventIDPrefix)
	if err != nil {
		return err
	}

	_, err = p.db.ExecContext(ctx, "SELECT handdraw.local_billing_event(?,?,?,?::jsonb)", id, eventID, event, string(raw))

	return err
}
