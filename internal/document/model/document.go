// Package model defines the versioned Cloud document independently of its CRDT encoding.
package model

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

const (
	// SchemaVersion is the Cloud content format understood by this server.
	SchemaVersion = 1
	// BoardIDPrefix identifies the document's owning board reference.
	BoardIDPrefix = "brd"
	// PageIDPrefix identifies pages independently of boards.
	PageIDPrefix = "pag"
	// NoteIDPrefix identifies Markdown note documents.
	NoteIDPrefix = "note"
	// FolderIDPrefix identifies note folders.
	FolderIDPrefix = "fld"
	// AssetIDPrefix identifies immutable uploads referenced by native files.
	AssetIDPrefix = "ast"
	// MaxDocumentBytes bounds the durable binary state.
	MaxDocumentBytes = 16 << 20
	// MaxUpdateBytes bounds an incoming incremental update.
	MaxUpdateBytes = 2 << 20
)

var (
	// ErrInvalidDocument means the content cannot safely enter the supported editor binding.
	ErrInvalidDocument = errors.New("invalid document schema")
	// ErrPendingDependencies requires a fresh complete sync without acknowledging the candidate.
	ErrPendingDependencies = errors.New("document has unresolved CRDT dependencies")
)

// Snapshot is the semantic projection of one board's durable CRDT state.
type Snapshot struct {
	SchemaVersion int               `json:"schemaVersion"`
	BoardID       string            `json:"boardId"`
	Pages         map[string]Page   `json:"pages"`
	PageOrder     []string          `json:"pageOrder"`
	Notes         map[string]string `json:"notes"`
	Folders       map[string]Folder `json:"folders"`
	NoteFolders   map[string]string `json:"noteFolders"`
}

// Page preserves native scene payloads while keeping uploaded bytes outside PostgreSQL.
type Page struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Scene Scene  `json:"scene"`
}

// Scene contains native elements, persistent view settings and private asset references.
type Scene struct {
	Elements     map[string]map[string]any `json:"elements"`
	ElementOrder []string                  `json:"elementOrder"`
	AppState     map[string]any            `json:"appState"`
	Files        map[string]Asset          `json:"files"`
}

// Asset maps an editor-native file ID to a previously authorized immutable upload.
type Asset struct {
	AssetID  string `json:"assetId"`
	MIMEType string `json:"mimeType"`
}

// Folder retains hierarchy and editor timestamps; note assignment is stored separately.
type Folder struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	ParentID  *string `json:"parentId"`
	CreatedAt int64   `json:"createdAt"`
	UpdatedAt int64   `json:"updatedAt"`
}

// Validation binds content to its board and assets authorized by the application workflow.
type Validation struct {
	BoardID       string          `json:"board_id"`
	AllowedAssets map[string]bool `json:"-"`
	PremiumAssets map[string]bool `json:"-"`
}

// Validate checks supported structure without normalizing or dropping user content.
func (s Snapshot) Validate(scope Validation) error {
	if s.Notes == nil || s.Folders == nil || s.NoteFolders == nil || s.PageOrder == nil || s.SchemaVersion != SchemaVersion || s.BoardID != scope.BoardID || resourceid.Validate(s.BoardID, BoardIDPrefix) != nil || len(s.Pages) < 1 || len(s.Pages) > 100 || len(s.Notes) > 1000 || len(s.Folders) > 1000 || len(s.PageOrder) != len(s.Pages) {
		return ErrInvalidDocument
	}

	seen := map[string]bool{}
	for _, id := range s.PageOrder {
		if seen[id] {
			return ErrInvalidDocument
		}

		seen[id] = true
		if _, ok := s.Pages[id]; !ok {
			return ErrInvalidDocument
		}
	}

	for id, p := range s.Pages {
		if id != p.ID || resourceid.Validate(id, PageIDPrefix) != nil || !validName(p.Name) {
			return ErrInvalidDocument
		}

		if err := p.Scene.Validate(scope); err != nil {
			return err
		}
	}

	for id, body := range s.Notes {
		if resourceid.Validate(id, NoteIDPrefix) != nil || !utf8.ValidString(body) || len(body) > 1<<20 || strings.ContainsRune(body, 0) {
			return ErrInvalidDocument
		}

		if err := validateNote(body); err != nil {
			return err
		}
	}

	for id, f := range s.Folders {
		if id != f.ID || resourceid.Validate(id, FolderIDPrefix) != nil || !validName(f.Name) || f.CreatedAt < 0 || f.UpdatedAt < f.CreatedAt {
			return ErrInvalidDocument
		}

		visited := map[string]bool{id: true}

		current := f.ParentID
		for current != nil {
			parent, ok := s.Folders[*current]
			if !ok || visited[*current] {
				return ErrInvalidDocument
			}

			visited[*current] = true
			current = parent.ParentID
		}
	}

	for note, folder := range s.NoteFolders {
		if _, ok := s.Notes[note]; !ok {
			return ErrInvalidDocument
		}

		if _, ok := s.Folders[folder]; !ok {
			return ErrInvalidDocument
		}
	}

	return nil
}

func validName(s string) bool {
	return valx.Var(s, valx.Required, valx.UTF8Text, valx.Length(1, 120)) == nil && strings.TrimSpace(s) == s
}
