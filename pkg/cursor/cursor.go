// Package cursor authenticates keyset positions together with their complete query scope.
package cursor

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// ErrInvalid indicates an invalid token, scope, key or keyset position.
var ErrInvalid = errors.New("invalid cursor")

// Scope prevents reuse across actors, resources, workspace filters and ordering.
type Scope struct {
	ActorID        string `json:"actor"`
	WorkspaceID    string `json:"workspace"`
	ResourcePrefix string `json:"resource"`
	Filter         string `json:"filter"`
	Order          string `json:"order"`
}

// Position carries the database timestamp and stable tie-breaker returned by a repository.
type Position struct {
	UpdatedAt time.Time `json:"updated_at"`
	ID        string    `json:"id"`
}

// Codec signs cursors with a server-held key shared across replicas.
type Codec struct{ key []byte }

// New requires at least 256 bits of key material and copies it to prevent caller mutation.
func New(key []byte) (*Codec, error) {
	if len(key) < 32 {
		return nil, ErrInvalid
	}

	return &Codec{key: bytes.Clone(key)}, nil
}

// Validate checks the trusted scope before signing or consuming a cursor.
func (s Scope) Validate() error {
	if resourceid.Validate(s.ActorID, "usr") != nil || (s.Order != "updated_at_desc_id_desc" && s.Order != "created_at_desc_id_desc" && s.Order != "id_asc") || len(s.Filter) > 512 {
		return ErrInvalid
	}

	if s.WorkspaceID != "" && resourceid.Validate(s.WorkspaceID, "ws") != nil {
		return ErrInvalid
	}

	switch s.ResourcePrefix {
	case "prj", "brd", "ws", "inv", "usr":
		return nil
	default:
		return ErrInvalid
	}
}

// Validate requires a canonical resource ID and a nonzero timestamp.
func (p Position) Validate(prefix string) error {
	if p.UpdatedAt.IsZero() || resourceid.Validate(p.ID, prefix) != nil {
		return ErrInvalid
	}

	return nil
}

// Encode signs a versioned position for precisely one authorized query scope.
func (c *Codec) Encode(scope Scope, position Position) (string, error) {
	if c == nil || len(c.key) < 32 || scope.Validate() != nil || position.Validate(scope.ResourcePrefix) != nil {
		return "", ErrInvalid
	}

	position.UpdatedAt = position.UpdatedAt.UTC()

	raw, err := json.Marshal(struct {
		Version  int      `json:"v"`
		Scope    Scope    `json:"scope"`
		Position Position `json:"position"`
	}{1, scope, position})
	if err != nil {
		return "", ErrInvalid
	}

	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(raw)

	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Decode verifies the signature before parsing and compares every scope field.
func (c *Codec) Decode(token string, scope Scope) (Position, error) {
	if c == nil || len(c.key) < 32 || scope.Validate() != nil || len(token) > 4096 {
		return Position{}, ErrInvalid
	}

	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return Position{}, ErrInvalid
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Position{}, ErrInvalid
	}

	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Position{}, ErrInvalid
	}

	mac := hmac.New(sha256.New, c.key)

	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return Position{}, ErrInvalid
	}

	var value struct {
		Version  int      `json:"v"`
		Scope    Scope    `json:"scope"`
		Position Position `json:"position"`
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	if err = decoder.Decode(&value); err != nil || value.Version != 1 || value.Scope != scope || value.Position.Validate(scope.ResourcePrefix) != nil {
		return Position{}, ErrInvalid
	}

	return value.Position, nil
}
