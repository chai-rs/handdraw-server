package model

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

const (
	// BoardIDPrefix identifies the owning board.
	BoardIDPrefix = "brd"
	// IDPrefix identifies private immutable assets.
	IDPrefix = "ast"
	// MaxAttachmentBytes is the published decimal 20 MB file cap.
	MaxAttachmentBytes int64 = 20_000_000
	// MaxImportBytes bounds an import source before decoding or expansion.
	MaxImportBytes int64 = 64_000_000
)

var (
	// ErrInvalid rejects unsupported files and malformed requests.
	ErrInvalid = errors.New("invalid asset")
	// ErrDenied rejects upload or download outside current access.
	ErrDenied = errors.New("asset permission denied")
	// ErrNotFound hides inaccessible assets.
	ErrNotFound = errors.New("asset not found")
	// ErrConflict rejects stale, expired or mismatched immutable uploads.
	ErrConflict = errors.New("asset conflict")
	// ErrQuota rejects reservations exceeding the current storage allocation.
	ErrQuota = errors.New("storage quota exceeded")
)

// Reserve declares expected bytes before accepting an upload; the server verifies all three fields.
type Reserve struct {
	Size    int64  `json:"size_bytes"`
	MIME    string `json:"mime_type"`
	SHA256  string `json:"sha256"`
	Purpose string `json:"purpose"`
}

// Validate bounds input independently of client-supplied file names or paths.
func (p Reserve) Validate() error {
	limit := MaxAttachmentBytes
	if p.Purpose == "import_source" {
		limit = MaxImportBytes
	} else if p.Purpose != "attachment" {
		return ErrInvalid
	}

	raw, err := hex.DecodeString(p.SHA256)
	if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != p.SHA256 || p.Size < 1 || p.Size > limit {
		return ErrInvalid
	}

	if valx.Var(p.MIME, valx.In("image/png", "image/jpeg", "image/webp", "image/gif", "image/svg+xml", "application/json")) != nil {
		return ErrInvalid
	}

	if (p.MIME == "application/json") != (p.Purpose == "import_source") {
		return ErrInvalid
	}

	return nil
}

// Asset contains public upload state, never an object path or provider credential.
type Asset struct {
	ID          string    `json:"id" bun:"id"`
	BoardID     string    `json:"board_id" bun:"board_id"`
	WorkspaceID string    `json:"-" bun:"workspace_id"`
	UploadedBy  string    `json:"uploaded_by" bun:"uploaded_by"`
	Purpose     string    `json:"purpose" bun:"purpose"`
	Status      string    `json:"status" bun:"status"`
	Size        int64     `json:"size_bytes" bun:"size_bytes"`
	MIME        string    `json:"mime_type" bun:"mime_type"`
	SHA256      string    `json:"sha256" bun:"sha256"`
	Premium     bool      `json:"premium" bun:"premium"`
	ExpiresAt   time.Time `json:"expires_at" bun:"expires_at"`
}

// Storage uses immutable keys derived only from validated asset IDs.
//
//mockery:generate: true
type Storage interface {
	Stage(context.Context, Asset, []byte) error
	Finalize(context.Context, Asset) error
	Read(context.Context, Asset) ([]byte, error)
	Remove(context.Context, string) error
}
