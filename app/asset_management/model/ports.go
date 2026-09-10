// Package model defines actor-scoped asset workflow persistence.
package model

import (
	"context"

	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
)

// Repository locks the workspace before all mutations and rechecks current access.
//
//mockery:generate: true
type Repository interface {
	Get(context.Context, string) (asset.Asset, error)
	Lock(context.Context, string, bool) error
	Reserve(context.Context, string, string, asset.Reserve) error
	Complete(context.Context, string) error
}
