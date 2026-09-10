// Package model defines durable import/export jobs and their bounded portable archive.
package model

import (
	"context"
	"errors"
	"time"

	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	document "github.com/chai-rs/handdraw-server/internal/document/model"
)

const (
	// IDPrefix identifies asynchronous transfer jobs.
	IDPrefix = "job"
	// BoardIDPrefix identifies the destination or exported board.
	BoardIDPrefix = "brd"
	// MaxAssets bounds archive expansion and object fan-out.
	MaxAssets = 100
)

var (
	// ErrInvalid rejects unsupported or lossy transfer input.
	ErrInvalid = errors.New("invalid transfer")
	// ErrDenied rejects a transfer after current authorization changes.
	ErrDenied = errors.New("transfer permission denied")
	// ErrNotFound hides inaccessible jobs.
	ErrNotFound = errors.New("transfer not found")
	// ErrConflict fences expired leases and changed job state.
	ErrConflict = errors.New("transfer conflict")
	// ErrEmpty means no eligible leased job is available.
	ErrEmpty = errors.New("no transfer jobs")
)

// Import starts only on an initializing board, using a previously finalized private source.
type Import struct {
	SourceAssetID       string `json:"source_asset_id"`
	Format              string `json:"format"`
	TargetSchemaVersion int    `json:"target_schema_version"`
}

// Export selects either a complete Handdraw archive or one native Excalidraw page.
type Export struct {
	Format        string `json:"format"`
	PageID        string `json:"page_id,omitempty"`
	IncludeAssets bool   `json:"include_assets"`
}

// Job is the public lifecycle projection; lease material and source bytes stay private.
type Job struct {
	ID            string  `json:"id" bun:"id"`
	BoardID       string  `json:"board_id" bun:"board_id"`
	Kind          string  `json:"kind" bun:"kind"`
	Status        string  `json:"status" bun:"status"`
	Attempts      int     `json:"attempts" bun:"attempts"`
	ResultAssetID *string `json:"result_asset_id" bun:"result_asset_id"`
	ErrorCode     *string `json:"error_code" bun:"error_code"`
}

// Task is returned exclusively by the narrow worker lease function.
type Task struct {
	ID    string `json:"id"`
	Actor string `json:"actor"`
	Token string `json:"token"`
}

// Payload captures an immutable export revision or authorized import source and its committed plan.
type Payload struct {
	ID            string        `json:"id"`
	BoardID       string        `json:"board_id"`
	WorkspaceID   string        `json:"workspace_id"`
	Actor         string        `json:"actor"`
	Kind          string        `json:"kind"`
	Format        string        `json:"format"`
	PageID        string        `json:"page_id"`
	IncludeAssets bool          `json:"include_assets"`
	State         []byte        `json:"state"`
	Source        *asset.Asset  `json:"source"`
	Assets        []asset.Asset `json:"assets"`
	Prepared      []asset.Asset `json:"prepared"`
	Premium       bool          `json:"premium"`
	CreatedAt     time.Time     `json:"created_at"`
}

// Attachment carries verified bytes in a portable JSON archive; no URL is fetched during import.
type Attachment struct {
	MIME string `json:"mime_type"`
	Data []byte `json:"data"`
}

// Archive preserves every supported page, note, folder, shape link and embedded asset.
type Archive struct {
	Format   string                `json:"format"`
	Version  int                   `json:"version"`
	Snapshot document.Snapshot     `json:"snapshot"`
	Assets   map[string]Attachment `json:"assets"`
}

// Repository combines request operations with narrow worker transaction callbacks.
//
//mockery:generate: true
type Repository interface {
	Enqueue(context.Context, string, string, string, string, string, bool) error
	Get(context.Context, string) (Job, error)
	Lease(context.Context, string) (Task, error)
	Run(context.Context, Task, func(context.Context, Payload) error) error
	Prepare(context.Context, Task, []asset.Asset) error
	Complete(context.Context, Task, []byte) error
	Fail(context.Context, Task, string) error
}
