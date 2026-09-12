// Package db implements billing requests and function-only worker authority.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/driver/pgdriver"
)

// Repository keeps worker credentials separate from actor transactions.
type Repository struct{ worker *bun.DB }

var _ model.Repository = (*Repository)(nil)

// New accepts nil for a request-only repository.
func New(worker *bun.DB) *Repository { return &Repository{worker: worker} }

func mapped(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return model.ErrNotFound
	}

	var p pgdriver.Error
	if errors.As(err, &p) {
		switch p.Field('C') {
		case "HD404":
			return model.ErrNotFound
		case "HD403", "42501":
			return model.ErrDenied
		case "HD409", "23505":
			return model.ErrConflict
		case "HD400", "23514", "22P02":
			return model.ErrInvalid
		}
	}

	return err
}

// Request invokes only a fixed, actor-authorized function; prices and removal sets are SQL-owned.
func (*Repository) Request(ctx context.Context, w, action, id, key string, p model.Command) (json.RawMessage, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}

	var result []byte

	err = tx.NewRaw("SELECT handdraw.billing_request(?,?,?,?::uuid,?::jsonb)", w, action, id, key, string(body)).Scan(ctx, &result)

	return result, mapped(err)
}

// Check refuses elevated, request or table-writing worker credentials.
func (r *Repository) Check(ctx context.Context) error {
	if r.worker == nil {
		return model.ErrDenied
	}

	var valid bool

	err := r.worker.NewRaw(`SELECT NOT rolsuper AND NOT rolbypassrls AND NOT rolcreaterole AND NOT rolcreatedb AND pg_has_role(current_user,'handdraw_billing_runtime','MEMBER') AND NOT pg_has_role(current_user,'handdraw_request','MEMBER') AND NOT pg_has_role(current_user,'handdraw_access_owner','MEMBER') AND NOT has_table_privilege(current_user,'handdraw.subscriptions','UPDATE') AND NOT has_schema_privilege(current_user,'handdraw','CREATE') FROM pg_roles WHERE rolname=current_user`).Scan(ctx, &valid)
	if err != nil {
		return err
	}

	if !valid {
		return model.ErrDenied
	}

	return nil
}

// Lease obtains a durable operation reference and crash-recoverable lease.
func (r *Repository) Lease(ctx context.Context, token string) (model.Task, error) {
	var raw []byte

	err := r.worker.NewRaw("SELECT handdraw.lease_billing(?)", token).Scan(ctx, &raw)
	if err != nil {
		return model.Task{}, mapped(err)
	}

	if len(raw) == 0 || string(raw) == "null" {
		return model.Task{}, model.ErrEmpty
	}

	var task model.Task

	err = json.Unmarshal(raw, &task)

	return task, err
}

// Apply commits history and the guarded current entitlement together.
func (r *Repository) Apply(ctx context.Context, t model.Task, p model.Snapshot) error {
	if p.Provider == "polar" && p.SubscriptionID == "" {
		_, err := r.worker.ExecContext(ctx, "SELECT handdraw.apply_polar_checkout(?,?,?,?)", t.ID, t.Token, p.CheckoutID, p.CheckoutURL)

		return mapped(err)
	}

	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}

	if p.Provider == "polar" {
		_, err = r.worker.ExecContext(ctx, "SELECT handdraw.apply_polar_billing(?,?,?::jsonb)", t.ID, t.Token, string(raw))
	} else {
		_, err = r.worker.ExecContext(ctx, "SELECT handdraw.apply_billing(?,?,?::jsonb)", t.ID, t.Token, string(raw))
	}

	return mapped(err)
}

// Maintain advances retention episodes, local notices and the purge barrier in bounded batches.
func (r *Repository) Maintain(ctx context.Context) error {
	_, err := r.worker.ExecContext(ctx, "SELECT handdraw.billing_maintenance()")
	return mapped(err)
}

// PortalCustomer resolves a provider customer only after the request transaction proves current ownership.
func (*Repository) PortalCustomer(ctx context.Context, workspaceID string) (string, error) {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return "", err
	}

	var customerID string

	err = tx.NewRaw("SELECT handdraw.polar_portal_customer(?)", workspaceID).Scan(ctx, &customerID)

	return customerID, mapped(err)
}
