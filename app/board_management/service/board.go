// Package service coordinates board metadata, content initialization and fresh access in one request transaction.
package service

import (
	"context"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/board_management/model"
	board "github.com/chai-rs/handdraw-server/internal/board/model"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	job "github.com/chai-rs/handdraw-server/internal/job/model"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Service composes domain ports and a scoped cross-domain query.
type Service struct {
	boards   model.Boards
	projects model.Projects
	builder  model.Builder
	access   model.Access
	query    model.Query
	keys     idem.Repository
	jobs     job.Repository
}

// New receives adapters and domain services from executable wiring.
func New(boards model.Boards, projects model.Projects, builder model.Builder, access model.Access, query model.Query, keys idem.Repository, jobs job.Repository) *Service {
	return &Service{boards: boards, projects: projects, builder: builder, access: access, query: query, keys: keys, jobs: jobs}
}

// Get returns active board metadata with current actor capabilities.
func (s *Service) Get(ctx context.Context, id string) (model.BoardView, error) {
	d, err := s.access.Require(ctx, access.Target{BoardID: id}, access.ReadMetadata)
	if err != nil {
		return model.BoardView{}, err
	}

	b, err := s.boards.Get(ctx, d.Facts.Workspace.ID, id)
	if err != nil {
		return model.BoardView{}, err
	}

	v, err := s.query.SchemaVersion(ctx, id)

	return model.BoardView{Board: b, SchemaVersion: v, Capabilities: d.Capabilities, CanInsertPremium: d.CanInsertPremium}, err
}

// Create binds the generated board ID to a validated initial document before persisting either.
func (s *Service) Create(ctx context.Context, w, key string, p model.CreateBoard) (model.BoardView, error) {
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return model.BoardView{}, err
	}

	d, err := s.access.Require(ctx, access.Target{WorkspaceID: w}, access.EditContent)
	if err != nil {
		return model.BoardView{}, err
	}

	if err := s.query.LockWorkspace(ctx, w); err != nil {
		return model.BoardView{}, err
	}

	request, err := idem.New("board.create:"+w, key, w, p)
	if err != nil {
		return model.BoardView{}, err
	}

	ticket, err := s.keys.Begin(ctx, request)
	if err != nil {
		return model.BoardView{}, err
	}

	if ticket.Replayed {
		return s.Get(ctx, ticket.Reference)
	}

	actor := d.Facts.Member.UserID

	project := ""
	if p.ProjectID != nil {
		project = *p.ProjectID
	}

	status := board.StatusActive

	mode := p.Initialization
	if mode == "import" {
		status = board.StatusInitializing
		mode = "empty"
	}

	b, err := board.NewBoard(board.NewBoardParams{WorkspaceID: w, CreatedBy: actor, Name: p.Name, ProjectID: project, Status: status})
	if err != nil {
		return model.BoardView{}, err
	}

	initial, err := s.builder.Build(b.ID(), mode)
	if err != nil {
		return model.BoardView{}, err
	}

	b, err = s.boards.CreatePrepared(ctx, b, board.InitialDocument{State: initial.State, SchemaVersion: initial.SchemaVersion})
	if err != nil {
		return model.BoardView{}, err
	}

	if err = s.keys.Complete(ctx, ticket, b.ID(), 201); err != nil {
		return model.BoardView{}, err
	}

	return s.Get(ctx, b.ID())
}

// List requires content read access and attaches current capabilities to each returned board.
func (s *Service) List(ctx context.Context, w string, project *string, page board.PageRequest) ([]model.BoardView, *board.Position, error) {
	if _, err := s.access.Require(ctx, access.Target{WorkspaceID: w}, access.ReadContent); err != nil {
		return nil, nil, err
	}

	result, err := s.boards.List(ctx, w, project, page)
	if err != nil {
		return nil, nil, err
	}

	items := []model.BoardView{}

	for _, b := range result.Items {
		if b.Status() != board.StatusActive {
			continue
		}

		view, err := s.Get(ctx, b.ID())
		if err != nil {
			return nil, nil, err
		}

		items = append(items, view)
	}

	return items, result.Next, nil
}

// Update verifies current board permissions before domain revision and project checks.
func (s *Service) Update(ctx context.Context, id string, p board.BoardPatch, revision int64) (model.BoardView, error) {
	d, err := s.access.Require(ctx, access.Target{BoardID: id}, access.EditContent)
	if err != nil {
		return model.BoardView{}, err
	}

	if err = s.query.LockWorkspace(ctx, d.Facts.Workspace.ID); err != nil {
		return model.BoardView{}, err
	}

	if _, err = s.boards.Update(ctx, d.Facts.Workspace.ID, id, p, revision); err != nil {
		return model.BoardView{}, err
	}

	return s.Get(ctx, id)
}

// Document returns only committed content after current resource authorization.
func (s *Service) Document(ctx context.Context, id string) ([]byte, int, error) {
	if _, err := s.access.Require(ctx, access.Target{BoardID: id}, access.ReadContent); err != nil {
		return nil, 0, err
	}

	return s.query.Document(ctx, id)
}

// CreateProject deduplicates a normalized project name inside an editable workspace.
func (s *Service) CreateProject(ctx context.Context, w, name, key string) (board.Project, error) {
	name = string(board.ProjectName(name).Normalize())
	if err := board.ProjectName(name).Validate(); err != nil {
		return board.Project{}, err
	}

	d, err := s.access.Require(ctx, access.Target{WorkspaceID: w}, access.EditContent)
	if err != nil {
		return board.Project{}, err
	}

	if err := s.query.LockWorkspace(ctx, w); err != nil {
		return board.Project{}, err
	}

	request, err := idem.New("project.create:"+w, key, w, struct {
		Name string `json:"name"`
	}{name})
	if err != nil {
		return board.Project{}, err
	}

	ticket, err := s.keys.Begin(ctx, request)
	if err != nil {
		return board.Project{}, err
	}

	if ticket.Replayed {
		return s.projects.Get(ctx, w, ticket.Reference)
	}

	actor := d.Facts.Member.UserID

	p, err := s.projects.Create(ctx, board.NewProjectParams{WorkspaceID: w, CreatedBy: actor, Name: name})
	if err != nil {
		return board.Project{}, err
	}

	if err = s.keys.Complete(ctx, ticket, p.ID(), 201); err != nil {
		return board.Project{}, err
	}

	return p, nil
}

// ListProjects uses the domain's bounded keyset collection.
func (s *Service) ListProjects(ctx context.Context, w string, page board.PageRequest) (board.Page[board.Project], error) {
	if _, err := s.access.Require(ctx, access.Target{WorkspaceID: w}, access.ReadContent); err != nil {
		return board.Page[board.Project]{}, err
	}

	return s.projects.List(ctx, w, page)
}

func (s *Service) projectScope(ctx context.Context, id string) (model.ProjectScope, error) {
	if resourceid.Validate(id, board.ProjectIDPrefix) != nil {
		return model.ProjectScope{}, resourceid.ErrInvalid
	}

	p, err := s.query.ProjectScope(ctx, id)
	if err != nil {
		return p, err
	}

	_, err = s.access.Require(ctx, access.Target{WorkspaceID: p.WorkspaceID}, access.EditContent)
	if err != nil {
		return p, err
	}

	return p, s.query.LockWorkspace(ctx, p.WorkspaceID)
}

// RenameProject resolves its parent through RLS, never trusting a client workspace ID.
func (s *Service) RenameProject(ctx context.Context, id, name string, revision int64) (board.Project, error) {
	p, err := s.projectScope(ctx, id)
	if err != nil {
		return board.Project{}, err
	}

	if p.Deleted {
		return board.Project{}, board.ErrNotFound
	}

	return s.projects.Rename(ctx, p.WorkspaceID, id, name, revision)
}

// DeleteProject keeps repeated deletion harmless while requiring current parent access.
func (s *Service) DeleteProject(ctx context.Context, id string, revision int64) error {
	p, err := s.projectScope(ctx, id)
	if err != nil {
		return err
	}

	if p.Deleted {
		return nil
	}

	return s.projects.DeleteEmpty(ctx, p.WorkspaceID, id, revision)
}

// Delete marks the board unavailable and enqueues cleanup in the same request transaction.
func (s *Service) Delete(ctx context.Context, id string, revision int64) (job.Deletion, error) {
	if resourceid.Validate(id, board.BoardIDPrefix) != nil {
		return job.Deletion{}, resourceid.ErrInvalid
	}

	existing, err := s.jobs.Get(ctx, id)
	if err != nil {
		return job.Deletion{}, err
	}

	if existing.ID != "" {
		if _, err = s.access.Require(ctx, access.Target{WorkspaceID: existing.WorkspaceID}, access.EditContent); err != nil {
			return job.Deletion{}, err
		}

		return existing, nil
	}

	d, err := s.access.Require(ctx, access.Target{BoardID: id}, access.ReadMetadata)
	if err != nil {
		return job.Deletion{}, err
	}

	if _, err = s.access.Require(ctx, access.Target{WorkspaceID: d.Facts.Workspace.ID}, access.EditContent); err != nil {
		return job.Deletion{}, err
	}

	if err = s.query.LockWorkspace(ctx, d.Facts.Workspace.ID); err != nil {
		return job.Deletion{}, err
	}

	existing, err = s.jobs.Get(ctx, id)
	if err != nil {
		return job.Deletion{}, err
	}

	if existing.ID != "" {
		return existing, nil
	}

	if _, err = s.boards.MarkDeleting(ctx, d.Facts.Workspace.ID, id, revision); err != nil {
		return job.Deletion{}, err
	}

	return s.jobs.Enqueue(ctx, d.Facts.Workspace.ID, id)
}
