// Package db serializes idempotent requests with transaction advisory locks and actor-scoped records.
package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/chai-rs/handdraw-server/internal/idempotency/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
)

type repository struct{}

var _ model.Repository = (*repository)(nil)

// New requires the existing request transaction; no pool is retained.
func New() *repository { return &repository{} }

// Begin returns a current resource reference or reserves the key atomically with subsequent business writes.
func (*repository) Begin(ctx context.Context, r model.Request) (model.Ticket, error) {
	if err := r.Validate(); err != nil {
		return model.Ticket{}, err
	}

	tx, err := rlstx.Current(ctx)
	if err != nil {
		return model.Ticket{}, err
	}

	actor, err := rlstx.Actor(ctx)
	if err != nil {
		return model.Ticket{}, err
	}

	var locked bool
	if err = tx.NewRaw("SELECT pg_try_advisory_xact_lock(hashtextextended(?,0))", actor+"/"+r.Operation+"/"+r.Key).Scan(ctx, &locked); err != nil {
		return model.Ticket{}, model.ErrUnavailable
	}

	if !locked {
		return model.Ticket{}, model.ErrProcessing
	}

	if _, err = tx.ExecContext(ctx, "SELECT handdraw.expire_request_key(?,?::uuid)", r.Operation, r.Key); err != nil {
		return model.Ticket{}, model.ErrUnavailable
	}

	var row struct {
		ID        string          `bun:"id"`
		Hash      []byte          `bun:"request_hash"`
		Status    string          `bun:"status"`
		Reference json.RawMessage `bun:"result_ref"`
	}

	err = tx.NewRaw("SELECT id,request_hash,status,result_ref FROM handdraw.idempotency_records WHERE actor_user_id=? AND operation=? AND idempotency_key=?::uuid", actor, r.Operation, r.Key).Scan(ctx, &row)
	if err == nil {
		if !bytes.Equal(row.Hash, r.Hash[:]) {
			return model.Ticket{}, model.ErrConflict
		}

		if row.Status != "completed" {
			return model.Ticket{}, model.ErrProcessing
		}

		var ref struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(row.Reference, &ref) != nil || ref.ID == "" {
			return model.Ticket{}, model.ErrUnavailable
		}

		return model.Ticket{ID: row.ID, Reference: ref.ID, Replayed: true}, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return model.Ticket{}, model.ErrUnavailable
	}

	id, err := resourceid.New(model.IDPrefix)
	if err != nil {
		return model.Ticket{}, err
	}

	_, err = tx.ExecContext(ctx, "INSERT INTO handdraw.idempotency_records(id,actor_user_id,workspace_id,operation,idempotency_key,request_hash,status,expires_at) VALUES (?,?,NULLIF(?,''),?,?::uuid,?,'processing',clock_timestamp()+interval '24 hours')", id, actor, r.WorkspaceID, r.Operation, r.Key, r.Hash[:])
	if err != nil {
		return model.Ticket{}, model.ErrUnavailable
	}

	return model.Ticket{ID: id}, nil
}

// Complete persists a safe reference in the same transaction as the created resource.
func (*repository) Complete(ctx context.Context, t model.Ticket, reference string, status int) error {
	if t.Replayed || reference == "" || resourceid.Validate(t.ID, model.IDPrefix) != nil {
		return model.ErrInvalid
	}

	tx, err := rlstx.Current(ctx)
	if err != nil {
		return err
	}

	result, err := tx.ExecContext(ctx, "UPDATE handdraw.idempotency_records SET status='completed',result_ref=jsonb_build_object('id',?::text),response_status=? WHERE id=? AND status='processing'", reference, status, t.ID)
	if err != nil {
		return model.ErrUnavailable
	}

	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return model.ErrUnavailable
	}

	return nil
}
