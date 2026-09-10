// Package service checks stable discussion targets without changing collaborative content.
package service

import (
	"context"

	"github.com/chai-rs/handdraw-server/app/discussion/model"
	comment "github.com/chai-rs/handdraw-server/internal/comment/model"
	document "github.com/chai-rs/handdraw-server/internal/document/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Service binds guarded discussion writes and the shared document decoder.
type Service struct {
	repository model.Repository
	codec      document.Codec
}

// New composes the discussion workflow.
func New(repository model.Repository, codec document.Codec) *Service {
	return &Service{repository: repository, codec: codec}
}

// Create verifies a target against committed content under the workspace lock.
func (s *Service) Create(ctx context.Context, board string, p comment.Create) (comment.Thread, error) {
	if resourceid.Validate(board, comment.BoardIDPrefix) != nil || p.Validate() != nil {
		return comment.Thread{}, comment.ErrInvalid
	}

	if err := s.repository.Lock(ctx, board); err != nil {
		return comment.Thread{}, err
	}

	state, err := s.repository.Document(ctx, board)
	if err != nil {
		return comment.Thread{}, err
	}

	scope, err := s.repository.Scope(ctx, board)
	if err != nil {
		return comment.Thread{}, err
	}

	snapshot, err := s.codec.Decode(state, scope)
	if err != nil {
		return comment.Thread{}, err
	}

	if !exists(snapshot, p.Anchor) {
		return comment.Thread{}, comment.ErrInvalid
	}

	thread, err := resourceid.New(comment.ThreadIDPrefix)
	if err != nil {
		return comment.Thread{}, err
	}

	message, err := resourceid.New(comment.CommentIDPrefix)
	if err != nil {
		return comment.Thread{}, err
	}

	if err = s.repository.Create(ctx, thread, message, board, p); err != nil {
		return comment.Thread{}, err
	}

	return s.repository.Thread(ctx, thread)
}

func exists(s document.Snapshot, a comment.Anchor) bool {
	switch a.Kind {
	case "board":
		return true
	case "note":
		_, ok := s.Notes[a.NoteID]
		return ok
	case "page":
		_, ok := s.Pages[a.PageID]
		return ok
	case "shape":
		e, ok := s.Pages[a.PageID].Scene.Elements[a.ElementID]
		return ok && e["isDeleted"] != true
	default:
		return false
	}
}

// Threads lists a bounded board discussion page under RLS.
func (s *Service) Threads(ctx context.Context, board, status, after string, limit int) ([]comment.Thread, error) {
	if resourceid.Validate(board, comment.BoardIDPrefix) != nil || (status != "" && status != "open" && status != "resolved") || limit < 1 || limit > 101 {
		return nil, comment.ErrInvalid
	}

	return s.repository.Threads(ctx, board, status, after, limit)
}

// Comments verifies that the thread is readable before listing replies.
func (s *Service) Comments(ctx context.Context, thread, after string, limit int) ([]comment.Comment, error) {
	if resourceid.Validate(thread, comment.ThreadIDPrefix) != nil || limit < 1 || limit > 101 {
		return nil, comment.ErrInvalid
	}

	if _, err := s.repository.Thread(ctx, thread); err != nil {
		return nil, err
	}

	return s.repository.Comments(ctx, thread, after, limit)
}

// Reply attributes a new message to the transaction's verified actor.
func (s *Service) Reply(ctx context.Context, thread string, p comment.Body) (comment.Comment, error) {
	if resourceid.Validate(thread, comment.ThreadIDPrefix) != nil || p.Validate() != nil {
		return comment.Comment{}, comment.ErrInvalid
	}

	id, err := resourceid.New(comment.CommentIDPrefix)
	if err != nil {
		return comment.Comment{}, err
	}

	if err = s.repository.Reply(ctx, id, thread, p.Body); err != nil {
		return comment.Comment{}, err
	}

	return s.repository.Comment(ctx, id)
}

// Resolve changes only status; anchors have no mutation operation.
func (s *Service) Resolve(ctx context.Context, thread, status string, revision int64) (comment.Thread, error) {
	if resourceid.Validate(thread, comment.ThreadIDPrefix) != nil || (status != "open" && status != "resolved") || revision < 1 {
		return comment.Thread{}, comment.ErrInvalid
	}

	if err := s.repository.Resolve(ctx, thread, status, revision); err != nil {
		return comment.Thread{}, err
	}

	return s.repository.Thread(ctx, thread)
}

// Edit edits the author's text or writes a moderation tombstone.
func (s *Service) Edit(ctx context.Context, id string, p comment.Body, remove bool, revision int64) (comment.Comment, error) {
	if resourceid.Validate(id, comment.CommentIDPrefix) != nil || revision < 1 || (!remove && p.Validate() != nil) {
		return comment.Comment{}, comment.ErrInvalid
	}

	if err := s.repository.Edit(ctx, id, p.Body, remove, revision); err != nil {
		return comment.Comment{}, err
	}

	return s.repository.Comment(ctx, id)
}
