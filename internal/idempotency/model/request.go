// Package model defines transaction-scoped request deduplication without caching authorization or response bodies.
package model

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"
	"github.com/google/uuid"
)

// IDPrefix identifies durable idempotency records.
const IDPrefix = "idem"

// WorkspaceIDPrefix identifies the optional workspace scope of a request key.
const WorkspaceIDPrefix = "ws"

var (
	// ErrInvalid rejects malformed keys or operation inputs.
	ErrInvalid = errors.New("invalid idempotency request")
	// ErrConflict rejects a key reused for different normalized input.
	ErrConflict = errors.New("idempotency conflict")
	// ErrProcessing asks a concurrent caller to retry the same operation.
	ErrProcessing = errors.New("operation processing")
	// ErrUnavailable hides persistence details.
	ErrUnavailable = errors.New("idempotency unavailable")
)

// Request binds a client key to one actor-scoped operation and normalized payload hash.
type Request struct {
	Operation   string   `json:"operation"`
	Key         string   `json:"key"`
	WorkspaceID string   `json:"workspace_id"`
	Hash        [32]byte `json:"-"`
}

// New hashes the normalized payload; clients never supply the persisted hash or actor.
func New(operation, key, workspace string, payload any) (Request, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Request{}, ErrInvalid
	}

	if id, err := uuid.Parse(key); err == nil && len(key) == 36 {
		key = id.String()
	}

	r := Request{Operation: operation, Key: key, WorkspaceID: workspace, Hash: sha256.Sum256(b)}

	return r, r.Validate()
}

// Validate requires a nonzero UUID, bounded operation and optional canonical workspace ID.
func (r Request) Validate() error {
	id, err := uuid.Parse(r.Key)
	if err != nil || id == uuid.Nil || len(r.Key) != 36 || r.Key != id.String() || r.Hash == [32]byte{} {
		return ErrInvalid
	}

	if valx.Var(r.Operation, valx.Required, valx.RuneLength(1, 120)) != nil {
		return ErrInvalid
	}

	if r.WorkspaceID != "" && valx.NewIDRule("workspace", WorkspaceIDPrefix).Validate(r.WorkspaceID) != nil {
		return ErrInvalid
	}

	return nil
}

// Ticket stores only the created resource reference; replay must reload current access and metadata.
type Ticket struct {
	ID        string `json:"id"`
	Reference string `json:"reference"`
	Replayed  bool   `json:"replayed"`
}

// Repository participates in the caller's transaction so failures roll back both business writes and the key.
//
//mockery:generate: true
type Repository interface {
	Begin(context.Context, Request) (Ticket, error)
	Complete(context.Context, Ticket, string, int) error
}
