// Package model defines the ephemeral Local sharing protocol and resource budgets.
package model

import (
	"encoding/json"
	"errors"
	"time"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

const (
	// IDPrefix identifies an ephemeral sharing session, never a Cloud board.
	IDPrefix = "lsh"
	// UserIDPrefix identifies a verified profile supplied by the Auth boundary.
	UserIDPrefix = "usr"
	// MaxSnapshotBytes includes embedded images and notes in the ephemeral snapshot.
	MaxSnapshotBytes = 8 << 20
	// MaxStoredBytes bounds snapshots retained by the whole relay.
	MaxStoredBytes = 32 << 20
	// MaxConnections includes unauthenticated sockets.
	MaxConnections = 25
	// MaxPeers includes the one host and its Viewers.
	MaxPeers = 5
	// IdleTimeout ends inactive sessions independently of token renewal.
	IdleTimeout = 30 * time.Minute
	// TokenTTL requires fresh authenticated token exchange every five minutes.
	TokenTTL = 5 * time.Minute
)

var (
	// ErrEnded means a session cannot be recovered or revived.
	ErrEnded = errors.New("local share session ended")
	// ErrDenied rejects tokens or actions outside the admitted role.
	ErrDenied = errors.New("local share permission denied")
	// ErrInvalid rejects an unsupported frame or resource.
	ErrInvalid = errors.New("invalid local share request")
	// ErrBudget rejects session, connection or memory capacity exhaustion.
	ErrBudget = errors.New("local share capacity exceeded")
)

// Admission is returned only after verified sign-in; invitation secrets are returned only on creation.
type Admission struct {
	ID             string    `json:"id"`
	Token          string    `json:"token"`
	InviteToken    string    `json:"invite_token,omitempty"`
	Role           string    `json:"role"`
	TokenExpiresAt time.Time `json:"token_expires_at"`
	IdleExpiresAt  time.Time `json:"idle_expires_at"`
	ServerTime     time.Time `json:"server_time"`
}

// Event is Local protocol 1, separate from the durable Hocuspocus transport.
type Event struct {
	Revision        uint64          `json:"revision,omitempty"`
	Type            string          `json:"type"`
	ProtocolVersion int             `json:"protocol_version,omitempty"`
	Role            string          `json:"role,omitempty"`
	PeerID          string          `json:"peer_id,omitempty"`
	Peers           int             `json:"peers,omitempty"`
	IdleExpiresAt   time.Time       `json:"idle_expires_at"`
	ServerTime      time.Time       `json:"server_time"`
	Snapshot        json.RawMessage `json:"snapshot,omitempty"`
	Presence        json.RawMessage `json:"presence,omitempty"`
	Reason          string          `json:"reason,omitempty"`
}

// Subscription carries coalesced latest-state notifications and a separate reliable termination signal.
type Subscription struct {
	Role   string
	ID     string
	Events <-chan Event
	Ended  <-chan string
}

// Stats exposes counts only, never snapshot bytes or admission material.
type Stats struct{ Sessions, Connections, StoredBytes int }

// CreateRequest selects the only supported Local source schema.
type CreateRequest struct {
	SchemaVersion int `json:"schema_version"`
}

// Validate rejects unsupported source formats before allocating any relay memory.
func (p CreateRequest) Validate() error {
	if valx.Var(p.SchemaVersion, valx.In(1)) != nil {
		return ErrInvalid
	}

	return nil
}
