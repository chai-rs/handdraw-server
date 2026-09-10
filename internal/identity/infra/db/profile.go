// Package db resolves profiles through a narrowly granted database function before a user RLS scope exists.
package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/chai-rs/handdraw-server/internal/identity/model"
	"github.com/uptrace/bun"
)

type profileRepository struct{ db *bun.DB }

var _ model.ProfileRepository = (*profileRepository)(nil)

// NewProfileRepository requires a resolver-only connection, never a migration or service-role pool.
func NewProfileRepository(db *bun.DB) *profileRepository { return &profileRepository{db: db} }

type profileRow struct {
	ID          string    `bun:"id"`
	AuthUserID  string    `bun:"auth_user_id"`
	DisplayName string    `bun:"display_name"`
	CreatedAt   time.Time `bun:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at"`
}

// Resolve atomically creates or reads a live mapping without overwriting user-owned display fields.
func (r *profileRepository) Resolve(ctx context.Context, candidate model.Profile) (model.Profile, error) {
	var row profileRow

	err := r.db.NewRaw("SELECT id,auth_user_id,display_name,created_at,updated_at FROM handdraw.resolve_profile(?,?::uuid,?)", candidate.ID(), candidate.AuthUserID(), candidate.DisplayName()).Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Profile{}, model.ErrUnauthenticated
	}

	if err != nil {
		return model.Profile{}, model.ErrUnavailable
	}

	profile, err := model.RehydrateProfile(model.RehydrateProfileParams{ID: row.ID, AuthUserID: model.AuthSubject(row.AuthUserID), DisplayName: row.DisplayName, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	if err != nil {
		return model.Profile{}, model.ErrUnavailable
	}

	return profile, nil
}

// Check verifies that the resolver connection has the narrow privileges required by this adapter.
func (r *profileRepository) Check(ctx context.Context) error {
	var allowed bool

	err := r.db.NewRaw(`SELECT NOT rolsuper AND NOT rolbypassrls AND NOT rolcreatedb AND NOT rolcreaterole
 AND NOT has_table_privilege(current_user,'handdraw.profiles','SELECT,INSERT,UPDATE,DELETE')
 AND has_function_privilege(current_user,'handdraw.resolve_profile(text,uuid,text)','EXECUTE')
 FROM pg_roles WHERE rolname=current_user`).Scan(ctx, &allowed)
	if err != nil || !allowed {
		return model.ErrUnavailable
	}

	return nil
}
