package model

import (
	"context"
	"errors"
)

// ErrSession rejects invalid, expired or no-longer-authorized connections.
var ErrSession = errors.New("collaboration session unavailable")

// ErrRejected means the peer submitted an unauthorized or semantically invalid mutation.
var ErrRejected = errors.New("collaboration update rejected")

// ErrConflict requires every peer to reopen committed state after a competing write.
var ErrConflict = errors.New("document revision conflict")

// Access is computed from current authentication and board policy on every operation.
type Access struct {
	UserID       string       `json:"-"`
	Capabilities Capabilities `json:"capabilities"`
	IdleSeconds  int          `json:"idle_timeout_seconds"`
}

// Document contains only successfully committed CRDT history.
type Document struct {
	State    []byte `json:"-"`
	Revision int64  `json:"-"`
}

// Backend connects transport to authenticated durable operations, independent of database and identity domains.
//
//mockery:generate: true
type Backend interface {
	Check(context.Context, string, string, string) (Access, error)
	Load(context.Context, string, string, string) (Document, Access, error)
	Apply(context.Context, string, string, string, int64, []byte) (Document, Access, error)
}

// Controls encodes the negotiated application envelope without coupling transport to its adapter.
//
//mockery:generate: true
type Controls interface {
	Decode(string, []byte, bool) (Control, error)
	Encode(string, Control, bool) ([]byte, error)
}
