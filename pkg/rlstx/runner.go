package rlstx

import (
	"context"

	"github.com/uptrace/bun"
)

// Runner binds one restricted request pool to the application transaction port.
type Runner struct{ db *bun.DB }

// NewRunner never accepts or derives an actor until Run receives a verified profile.
func NewRunner(db *bun.DB) *Runner { return &Runner{db: db} }

// Run delegates cleanup and actor-local isolation to the shared transaction boundary.
func (r *Runner) Run(ctx context.Context, actor string, fn func(context.Context) error) error {
	return Run(ctx, r.db, actor, fn)
}
