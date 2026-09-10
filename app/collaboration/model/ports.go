// Package model defines the cross-domain collaboration persistence boundary.
package model

import (
	"context"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	collaboration "github.com/chai-rs/handdraw-server/internal/collaboration/model"
)

// Access resolves board permissions inside the current actor transaction.
//
//mockery:generate: true
type Access interface {
	Require(context.Context, access.Target, access.Action) (access.Decision, error)
}

// Documents serializes content writes against access changes and compares durable revisions.
//
//mockery:generate: true
type Documents interface {
	LockWorkspace(context.Context, string) error
	Load(context.Context, string) (collaboration.Document, error)
	Save(context.Context, string, int64, []byte) error
}
