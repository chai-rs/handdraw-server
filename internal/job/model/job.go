// Package model defines durable board cleanup without coupling jobs to provider integrations.
package model

import (
	"context"
	"errors"
)

// IDPrefix identifies background jobs.
const IDPrefix = "job"

// ErrUnavailable hides cleanup persistence failures.
var ErrUnavailable = errors.New("cleanup unavailable")

// Deletion records the durable resource reference even after the board has been removed.
type Deletion struct {
	ID          string `json:"id"`
	BoardID     string `json:"board_id"`
	WorkspaceID string `json:"-"`
	Status      string `json:"status"`
}

// Repository enqueues inside a board mutation transaction and reads only visible jobs.
//
//mockery:generate: true
type Repository interface {
	Enqueue(context.Context, string, string) (Deletion, error)
	Get(context.Context, string) (Deletion, error)
}

// Worker performs one bounded atomic cleanup; zero means no job is currently eligible.
//
//mockery:generate: true
type Worker interface {
	RunOne(context.Context) (int, error)
}
