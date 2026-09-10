package service

import (
	"context"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"

	"github.com/chai-rs/handdraw-server/internal/board/model"
)

// BoardService executes metadata operations; app/board_management supplies validated initial content.
type BoardService struct{ repository model.BoardRepository }

// NewBoardService binds board operations to persistence without cross-domain calls.
func NewBoardService(repository model.BoardRepository) *BoardService {
	return &BoardService{repository: repository}
}

// Create persists a board and its initial document together in the application transaction.
func (s *BoardService) Create(ctx context.Context, params model.NewBoardParams, document model.InitialDocument) (model.Board, error) {
	if err := document.Validate(); err != nil {
		return model.Board{}, err
	}

	board, err := model.NewBoard(params)
	if err != nil {
		return model.Board{}, err
	}

	return s.repository.Create(ctx, board, document)
}

// Get returns metadata scoped to its immutable workspace.
func (s *BoardService) Get(ctx context.Context, workspace, id string) (model.Board, error) {
	if err := (model.BoardReference{WorkspaceID: workspace, ID: id}).Validate(); err != nil {
		return model.Board{}, err
	}

	return s.repository.Get(ctx, workspace, id)
}

// List accepts nil for all projects, an empty project ID for ungrouped boards, or an exact project ID.
func (s *BoardService) List(ctx context.Context, workspace string, projectID *string, page model.PageRequest) (model.Page[model.Board], error) {
	if err := valx.NewIDRule("workspace", model.WorkspaceIDPrefix).Validate(workspace); err != nil {
		return model.Page[model.Board]{}, err
	}

	if projectID != nil && *projectID != "" {
		if err := valx.NewIDRule("project", model.ProjectIDPrefix).Validate(*projectID); err != nil {
			return model.Page[model.Board]{}, err
		}
	}

	page, err := page.Normalize(model.BoardIDPrefix)
	if err != nil {
		return model.Page[model.Board]{}, err
	}

	return s.repository.List(ctx, workspace, projectID, page)
}

// Update preserves omitted fields and validates the expected metadata revision.
func (s *BoardService) Update(ctx context.Context, workspace, id string, patch model.BoardPatch, expectedRevision int64) (model.Board, error) {
	if err := (model.BoardReference{WorkspaceID: workspace, ID: id}).Validate(); err != nil {
		return model.Board{}, err
	}

	if err := model.ExpectedRevision(expectedRevision).Validate(); err != nil {
		return model.Board{}, err
	}

	patch = patch.Normalize()
	if err := patch.Validate(); err != nil {
		return model.Board{}, err
	}

	return s.repository.Update(ctx, workspace, id, patch, expectedRevision)
}

// MarkDeleting is the metadata step of the application's atomic deletion-job workflow.
func (s *BoardService) MarkDeleting(ctx context.Context, workspace, id string, expectedRevision int64) (model.Board, error) {
	if err := (model.BoardReference{WorkspaceID: workspace, ID: id}).Validate(); err != nil {
		return model.Board{}, err
	}

	if err := model.ExpectedRevision(expectedRevision).Validate(); err != nil {
		return model.Board{}, err
	}

	return s.repository.MarkDeleting(ctx, workspace, id, expectedRevision)
}

// CreatePrepared persists an identified board after the application builds content for that exact ID.
func (s *BoardService) CreatePrepared(ctx context.Context, b model.Board, d model.InitialDocument) (model.Board, error) {
	if err := (model.BoardReference{ID: b.ID(), WorkspaceID: b.WorkspaceID()}).Validate(); err != nil {
		return model.Board{}, err
	}

	if err := d.Validate(); err != nil {
		return model.Board{}, err
	}

	return s.repository.Create(ctx, b, d)
}
