// Package db invokes the narrow bootstrap function in the authenticated request transaction.
package db

import (
	"context"

	"github.com/chai-rs/handdraw-server/app/onboarding/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
)

type repository struct{}

var _ model.Repository = (*repository)(nil)

// New constructs the transaction-only bootstrap adapter.
func New() *repository { return &repository{} }

// Create initializes Owner and automatic usage counters; no subscription or payment entitlement is created.
func (*repository) Create(ctx context.Context, id string, p model.Params) error {
	tx, err := rlstx.Current(ctx)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, "SELECT handdraw.bootstrap_workspace(?,?,?)", id, p.Name, p.Kind)
	if err != nil {
		return workspace.ErrUnavailable
	}

	return nil
}
