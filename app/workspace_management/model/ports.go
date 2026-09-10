// Package model states the workspace application's domain and policy dependencies.
package model

import (
	"context"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
)

// Workspaces owns metadata operations without cross-domain queries.
//
//mockery:generate: true
type Workspaces interface {
	List(context.Context, workspace.PageRequest) (workspace.Page, error)
	Rename(context.Context, string, workspace.Name, int64) (workspace.Workspace, error)
}

// Access reevaluates permissions for every operation.
//
//mockery:generate: true
type Access interface {
	Require(context.Context, access.Target, access.Action) (access.Decision, error)
}

// Session verifies a token and commits the callback before a response is sent.
//
//mockery:generate: true
type Session interface {
	Run(context.Context, identity.AccessToken, func(context.Context) error) error
}

// Onboarding creates only the current actor's empty workspace with idempotent retries.
//
//mockery:generate: true
type Onboarding interface {
	Create(context.Context, string, string, string) (access.Decision, error)
}
