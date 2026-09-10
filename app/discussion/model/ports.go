// Package model defines discussion persistence and content-validation boundaries.
package model

import (
	"context"

	comment "github.com/chai-rs/handdraw-server/internal/comment/model"
	document "github.com/chai-rs/handdraw-server/internal/document/model"
)

// Repository requires an actor-scoped transaction for every operation.
//
//mockery:generate: true
type Repository interface {
	Scope(context.Context, string) (document.Validation, error)
	Lock(context.Context, string) error
	Document(context.Context, string) ([]byte, error)
	Thread(context.Context, string) (comment.Thread, error)
	Comment(context.Context, string) (comment.Comment, error)
	Threads(context.Context, string, string, string, int) ([]comment.Thread, error)
	Comments(context.Context, string, string, int) ([]comment.Comment, error)
	Create(context.Context, string, string, string, comment.Create) error
	Reply(context.Context, string, string, string) error
	Resolve(context.Context, string, string, int64) error
	Edit(context.Context, string, string, bool, int64) error
}
