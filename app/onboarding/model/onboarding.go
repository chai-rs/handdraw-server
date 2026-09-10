// Package model defines authenticated workspace bootstrap inputs and application ports.
package model

import (
	"context"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

// Params contains only workspace metadata; billing and actor are never client inputs.
type Params struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Normalize canonicalizes the name before hashing the idempotent request.
func (p Params) Normalize() Params { p.Name = string(workspace.Name(p.Name).Normalize()); return p }

// Validate accepts personal or team metadata and the workspace naming rule.
func (p Params) Validate() error {
	if workspace.Name(p.Name).Validate() != nil || valx.Var(p.Kind, valx.In("personal", "team")) != nil {
		return workspace.ErrInvalid
	}

	return nil
}

// Repository creates workspace and matching Owner through the restricted bootstrap function.
//
//mockery:generate: true
type Repository interface {
	Create(context.Context, string, Params) error
}

// Access reloads membership and entitlement for both new requests and retries.
//
//mockery:generate: true
type Access interface {
	Require(context.Context, access.Target, access.Action) (access.Decision, error)
}
