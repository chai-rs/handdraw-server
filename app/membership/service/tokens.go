package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"

	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Tokens keeps invitation bearer secrets reproducible for delivery retries without storing plaintext.
type Tokens struct{ key []byte }

// NewTokens requires an independent 256-bit secret; changing it invalidates undelivered invitations.
func NewTokens(key []byte) (*Tokens, error) {
	if len(key) < 32 {
		return nil, workspace.ErrInvalid
	}

	return &Tokens{key: append([]byte(nil), key...)}, nil
}

// Issue domain-separates invitation tokens from any other HMAC consumer.
func (t *Tokens) Issue(id string) (workspace.InvitationToken, error) {
	if err := resourceid.Validate(id, workspace.InvitationIDPrefix); err != nil {
		return "", err
	}

	mac := hmac.New(sha256.New, t.key)
	_, _ = mac.Write([]byte("handdraw.invitation.v1\x00" + id))

	return workspace.InvitationToken(base64.RawURLEncoding.EncodeToString(mac.Sum(nil))), nil
}
