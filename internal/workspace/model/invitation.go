package model

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

const (
	// InvitationIDPrefix identifies an invitation, independently of its bearer token.
	InvitationIDPrefix = "inv"
	// BoardIDPrefix identifies an external board reference.
	BoardIDPrefix = "brd"
)

var (
	// ErrCapacity prevents allocating an Editor seat twice or changing a pending invitation implicitly.
	ErrCapacity = errors.New("membership capacity or state conflict")
	// ErrInvitationGone rejects consumed, expired or revoked invitation tokens.
	ErrInvitationGone = errors.New("invitation is no longer pending")
)

// InviteParams contains normalized recipient input; only Owner workflows may apply it.
type InviteParams struct {
	Email string `json:"email"`
	Role  Role   `json:"role"`
}

// Normalize applies the same email comparison used by the Auth-backed SQL guard.
func (p InviteParams) Normalize() InviteParams {
	p.Email = strings.ToLower(strings.TrimSpace(p.Email))
	return p
}

// Validate refuses Owner invitations and noncanonical recipient input.
func (p InviteParams) Validate() error {
	if p != p.Normalize() || valx.Struct(&p, valx.Field(&p.Email, valx.Required, valx.Length(3, 254), valx.EmailFormat), valx.Field(&p.Role, valx.In(Editor, Viewer))) != nil {
		return ErrInvalid
	}

	return nil
}

// Invitation is the public Owner-only projection; bearer material never appears in responses.
type Invitation struct {
	ID          string    `json:"id" bun:"id"`
	WorkspaceID string    `json:"workspace_id" bun:"workspace_id"`
	BoardID     *string   `json:"board_id" bun:"board_id"`
	Scope       string    `json:"scope" bun:"scope"`
	Email       string    `json:"email" bun:"email_normalized"`
	Role        Role      `json:"role" bun:"role"`
	Status      string    `json:"status" bun:"status"`
	ExpiresAt   time.Time `json:"expires_at" bun:"expires_at"`
	CreatedAt   time.Time `json:"created_at" bun:"created_at"`
}

// InvitationToken is a 256-bit bearer secret; transport accepts it only in a POST body.
type InvitationToken string

// Validate requires the canonical unpadded base64url encoding.
func (t InvitationToken) Validate() error {
	raw, err := base64.RawURLEncoding.DecodeString(string(t))
	if err != nil || len(raw) != sha256.Size || base64.RawURLEncoding.EncodeToString(raw) != string(t) {
		return ErrInvalid
	}

	return nil
}

// Digest yields the sole token representation stored in PostgreSQL.
func (t InvitationToken) Digest() ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}

	sum := sha256.Sum256([]byte(t))

	return sum[:], nil
}

// RoleChange carries the target and its strong membership revision precondition.
type RoleChange struct {
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	Role        *Role  `json:"role"`
	Revision    int64  `json:"revision,string"`
}

// Validate reserves a nil role for deletion and never permits editing an Owner role.
func (p RoleChange) Validate() error {
	if resourceid.Validate(p.WorkspaceID, WorkspaceIDPrefix) != nil || resourceid.Validate(p.UserID, UserIDPrefix) != nil || p.Revision < 1 {
		return ErrInvalid
	}

	if p.Role != nil && valx.Var(*p.Role, valx.In(Editor, Viewer)) != nil {
		return ErrInvalid
	}

	return nil
}
