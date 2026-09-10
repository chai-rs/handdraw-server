// Package model defines stable discussion anchors and author-owned messages.
package model

import (
	"errors"
	"strings"
	"time"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

const (
	// ThreadIDPrefix identifies a discussion independently of its target.
	ThreadIDPrefix = "thr"
	// CommentIDPrefix identifies a message, including a deleted tombstone.
	CommentIDPrefix = "cmt"
	// BoardIDPrefix identifies the containing board.
	BoardIDPrefix = "brd"
	// PageIDPrefix identifies a page anchor.
	PageIDPrefix = "pag"
	// NoteIDPrefix identifies a note anchor.
	NoteIDPrefix = "note"
)

var (
	// ErrInvalid rejects malformed requests.
	ErrInvalid = errors.New("invalid discussion")
	// ErrNotFound hides inaccessible resources.
	ErrNotFound = errors.New("discussion not found")
	// ErrDenied rejects changes outside the author's or Owner's authority.
	ErrDenied = errors.New("discussion permission denied")
	// ErrConflict requires a fresh resource revision.
	ErrConflict = errors.New("discussion revision conflict")
)

// Anchor identifies content by immutable identity, never screen coordinates.
type Anchor struct {
	Kind      string `json:"kind"`
	PageID    string `json:"page_id,omitempty"`
	ElementID string `json:"element_id,omitempty"`
	NoteID    string `json:"note_id,omitempty"`
	Label     string `json:"label,omitempty"`
}

// Validate permits exactly the fields required by the selected anchor kind.
func (a Anchor) Validate() error {
	if valx.Var(a.Label, valx.UTF8Text, valx.Length(0, 240)) != nil {
		return ErrInvalid
	}

	switch a.Kind {
	case "board":
		if a.PageID == "" && a.ElementID == "" && a.NoteID == "" {
			return nil
		}
	case "page":
		if resourceid.Validate(a.PageID, PageIDPrefix) == nil && a.ElementID == "" && a.NoteID == "" {
			return nil
		}
	case "shape":
		if resourceid.Validate(a.PageID, PageIDPrefix) == nil && a.NoteID == "" && valx.Var(a.ElementID, valx.Required, valx.UTF8Text, valx.Length(1, 256)) == nil {
			return nil
		}
	case "note":
		if resourceid.Validate(a.NoteID, NoteIDPrefix) == nil && a.PageID == "" && a.ElementID == "" {
			return nil
		}
	}

	return ErrInvalid
}

// Body is plain text; it never writes into the collaborative note document.
type Body struct {
	Body string `json:"body"`
}

// Validate bounds text and rejects blank or invalid Unicode messages.
func (b Body) Validate() error {
	if strings.TrimSpace(b.Body) == "" || valx.Var(b.Body, valx.Required, valx.UTF8Text, valx.Length(1, 4000)) != nil {
		return ErrInvalid
	}

	return nil
}

// Create atomically creates a thread and its first message.
type Create struct {
	Anchor Anchor `json:"anchor"`
	Body   string `json:"body"`
}

// Validate checks both immutable target identity and message content.
func (p Create) Validate() error {
	if p.Anchor.Validate() != nil {
		return ErrInvalid
	}

	return (Body{Body: p.Body}).Validate()
}

// Thread keeps its original anchor even when the target is later deleted.
type Thread struct {
	ID        string    `json:"id" bun:"id"`
	BoardID   string    `json:"board_id" bun:"board_id"`
	Anchor    Anchor    `json:"anchor" bun:"anchor,type:jsonb"`
	CreatedBy string    `json:"created_by" bun:"created_by"`
	Status    string    `json:"status" bun:"status"`
	Revision  int64     `json:"revision,string" bun:"revision"`
	CreatedAt time.Time `json:"created_at" bun:"created_at"`
	UpdatedAt time.Time `json:"updated_at" bun:"updated_at"`
}

// Comment preserves attribution and timestamps after its text is removed.
type Comment struct {
	ID        string     `json:"id" bun:"id"`
	BoardID   string     `json:"board_id" bun:"board_id"`
	ThreadID  string     `json:"thread_id" bun:"thread_id"`
	AuthorID  string     `json:"author_user_id" bun:"author_user_id"`
	Body      *string    `json:"body" bun:"body"`
	Revision  int64      `json:"revision,string" bun:"revision"`
	CreatedAt time.Time  `json:"created_at" bun:"created_at"`
	UpdatedAt time.Time  `json:"updated_at" bun:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at" bun:"deleted_at"`
}
