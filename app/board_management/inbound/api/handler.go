// Package api exposes authenticated project and board workflows through the shared HTTP contract.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/board_management/model"
	"github.com/chai-rs/handdraw-server/app/board_management/service"
	membership "github.com/chai-rs/handdraw-server/app/membership/model"
	board "github.com/chai-rs/handdraw-server/internal/board/model"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	"github.com/chai-rs/handdraw-server/pkg/cursor"

	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/gofiber/fiber/v3"
)

// Session opens the request transaction only after verified identity resolution.
//
//mockery:generate: true
type Session interface {
	Run(context.Context, identity.AccessToken, func(context.Context) error) error
}

// SharedBoards supplies the actor-scoped explicit board grants.
//
//mockery:generate: true
type SharedBoards interface {
	SharedBoardIDs(context.Context, membership.PageRequest) ([]string, *cursor.Position, error)
}

// Handler serializes workflow results only after their request transaction commits.
type Handler struct {
	session  Session
	service  *service.Service
	cursors  *cursor.Codec
	shared   SharedBoards
	deletion bool
}

// New wires the API; deletion is enabled only alongside a configured cleanup worker.
func New(session Session, service *service.Service, cursors *cursor.Codec, deletion bool) *Handler {
	return &Handler{session: session, service: service, cursors: cursors, deletion: deletion}
}

// WithSharedBoards enables the grant-only discovery endpoint when membership is configured.
func (h *Handler) WithSharedBoards(shared SharedBoards) *Handler { h.shared = shared; return h }

// Register mounts supported operations; shared grants and imports remain unavailable.
func (h *Handler) Register(r fiber.Router) {
	if h.shared != nil {
		r.Get("/me/shared-boards", h.SharedBoards)
	}

	r.Get("/workspaces/:workspace_id/projects", h.ListProjects)
	r.Post("/workspaces/:workspace_id/projects", h.CreateProject)
	r.Patch("/projects/:project_id", h.RenameProject)
	r.Delete("/projects/:project_id", h.DeleteProject)
	r.Get("/workspaces/:workspace_id/boards", h.ListBoards)
	r.Post("/workspaces/:workspace_id/boards", h.CreateBoard)
	r.Get("/boards/:board_id", h.GetBoard)
	r.Get("/boards/:board_id/document", h.Document)
	r.Patch("/boards/:board_id", h.UpdateBoard)

	if h.deletion {
		r.Delete("/boards/:board_id", h.DeleteBoard)
	}
}

type boardResponse struct {
	ID            string              `json:"id"`
	WorkspaceID   string              `json:"workspace_id"`
	ProjectID     *string             `json:"project_id"`
	Name          string              `json:"name"`
	Status        board.Status        `json:"status"`
	Revision      int64               `json:"revision,string"`
	SchemaVersion int                 `json:"document_schema_version"`
	Capabilities  access.Capabilities `json:"capabilities"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
}
type projectResponse struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Revision    int64     `json:"revision,string"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func boardView(v model.BoardView) boardResponse {
	b := v.Board

	var project *string

	if b.ProjectID() != "" {
		p := b.ProjectID()
		project = &p
	}

	return boardResponse{ID: b.ID(), WorkspaceID: b.WorkspaceID(), ProjectID: project, Name: b.Name(), Status: b.Status(), Revision: b.Revision(), SchemaVersion: v.SchemaVersion, Capabilities: v.Capabilities, CreatedAt: b.CreatedAt(), UpdatedAt: b.UpdatedAt()}
}

func projectView(p board.Project) projectResponse {
	return projectResponse{ID: p.ID(), WorkspaceID: p.WorkspaceID(), Name: p.Name(), Revision: p.Revision(), CreatedAt: p.CreatedAt(), UpdatedAt: p.UpdatedAt()}
}

func (h *Handler) within(c fiber.Ctx, fn func(context.Context) error) error {
	headers := c.Request().Header.PeekAll("Authorization")
	if len(headers) != 1 {
		return identity.ErrUnauthenticated
	}

	parts := strings.Fields(string(headers[0]))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return identity.ErrUnauthenticated
	}

	return h.session.Run(c.Context(), identity.AccessToken(parts[1]), fn)
}

var errMedia = errors.New("unsupported media type")

func bind(c fiber.Ctx, v any) error {
	if !strings.EqualFold(strings.TrimSpace(strings.Split(c.Get("Content-Type"), ";")[0]), "application/json") {
		return errMedia
	}

	if err := c.Bind().Body(v); err != nil {
		if errors.Is(err, model.ErrImportUnavailable) {
			return err
		}

		return board.ErrInvalidState
	}

	return nil
}

func key(c fiber.Ctx) (string, error) {
	if len(c.Request().Header.PeekAll("Idempotency-Key")) != 1 {
		return "", idem.ErrInvalid
	}

	return c.Get("Idempotency-Key"), nil
}

func revision(c fiber.Ctx) (int64, error) {
	r, err := fx.ParseIfMatch(c.Get("If-Match"))
	if err != nil {
		return 0, err
	}

	if len(c.Request().Header.PeekAll("If-Match")) != 1 {
		return 0, fx.ErrInvalidPrecondition
	}

	return r, nil
}
func etag(c fiber.Ctx, r int64) { value, _ := fx.StrongETag(r); c.Set("ETag", value) }
func outcome(c fiber.Ctx, err error, operation string) error {
	if errors.Is(err, idem.ErrProcessing) {
		c.Status(202)
		return fx.Success(c, fiber.Map{"operation": operation, "status": "processing"})
	}

	return publicError(err)
}

func publicError(err error) error {
	if err == nil {
		return nil
	}

	status, code, message := 503, "dependency_unavailable", "Service unavailable"

	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		status, code, message = 401, "unauthenticated", "Authentication required"
	case errors.Is(err, access.ErrNotFound), errors.Is(err, board.ErrNotFound):
		status, code, message = 404, "not_found", "Resource not found"
	case errors.Is(err, access.ErrDenied), errors.Is(err, board.ErrPermissionDenied):
		status, code, message = 403, "permission_denied", "Permission denied"
	case errors.Is(err, board.ErrRevisionConflict):
		status, code, message = 412, "revision_conflict", "Reload the current resource revision"
	case errors.Is(err, fx.ErrPreconditionRequired):
		status, code, message = 428, "precondition_required", "If-Match is required"
	case errors.Is(err, idem.ErrConflict):
		status, code, message = 409, "idempotency_conflict", "Key already used for different input"
	case errors.Is(err, board.ErrProjectNotEmpty):
		status, code, message = 409, "project_not_empty", "Move or delete the project boards first"
	case errors.Is(err, model.ErrImportUnavailable):
		status, code, message = 422, "invalid_document_schema", "Initialization mode is not supported"
	case errors.Is(err, errMedia):
		status, code, message = 415, "unsupported_media_type", "Content-Type must be application/json"
	case errors.Is(err, cursor.ErrInvalid):
		status, code, message = 400, "invalid_cursor", "Invalid cursor"
	case errors.Is(err, resourceid.ErrInvalid):
		status, code, message = 400, "invalid_resource_id", "Invalid resource ID"
	case errors.Is(err, board.ErrInvalidName), errors.Is(err, board.ErrInvalidPage), errors.Is(err, board.ErrInvalidProject), errors.Is(err, board.ErrInvalidState), errors.Is(err, board.ErrInvalidRevision), errors.Is(err, fx.ErrInvalidPrecondition), errors.Is(err, idem.ErrInvalid):
		status, code, message = 400, "invalid_request", "Invalid request"
	}

	return fx.RequestError(status, code, message, err)
}

// GetBoard returns current metadata and effective capabilities.
func (h *Handler) GetBoard(c fiber.Ctx) error {
	var v model.BoardView

	err := h.within(c, func(ctx context.Context) error {
		var err error

		v, err = h.service.Get(ctx, c.Params("board_id"))

		return err
	})
	if err != nil {
		return publicError(err)
	}

	etag(c, v.Board.Revision())

	return fx.Success(c, boardView(v))
}

// CreateBoard deduplicates supported initial content with its metadata.
func (h *Handler) CreateBoard(c fiber.Ctx) error {
	var v model.BoardView

	err := h.within(c, func(ctx context.Context) error {
		var body struct {
			Name           string          `json:"name"`
			Project        json.RawMessage `json:"project_id"`
			Initialization string          `json:"initialization"`
		}
		if err := bind(c, &body); err != nil {
			return err
		}

		if body.Project == nil {
			return board.ErrInvalidProject
		}

		p := model.CreateBoard{Name: body.Name, Initialization: body.Initialization}
		if string(body.Project) != "null" {
			var id string
			if json.Unmarshal(body.Project, &id) != nil || id == "" {
				return board.ErrInvalidProject
			}

			p.ProjectID = &id
		}

		if p.Name != string(board.BoardName(p.Name).Normalize()) {
			return board.ErrInvalidName
		}

		key, err := key(c)
		if err != nil {
			return err
		}

		v, err = h.service.Create(ctx, c.Params("workspace_id"), key, p)

		return err
	})
	if err != nil {
		return outcome(c, err, "board.create")
	}

	etag(c, v.Board.Revision())

	return fx.Created(c, "/v1/boards/"+v.Board.ID(), boardView(v))
}

// UpdateBoard distinguishes an omitted project from an explicit null assignment.
func (h *Handler) UpdateBoard(c fiber.Ctx) error {
	var v model.BoardView

	err := h.within(c, func(ctx context.Context) error {
		r, err := revision(c)
		if err != nil {
			return err
		}

		var body struct {
			Name    json.RawMessage `json:"name"`
			Project json.RawMessage `json:"project_id"`
		}
		if err = bind(c, &body); err != nil {
			return err
		}

		p := board.BoardPatch{}

		if body.Name != nil {
			var name string
			if string(body.Name) == "null" || json.Unmarshal(body.Name, &name) != nil || name != string(board.BoardName(name).Normalize()) {
				return board.ErrInvalidName
			}

			p.Name = &name
		}

		if body.Project != nil {
			p.Project.Set = true
			if string(body.Project) != "null" {
				if json.Unmarshal(body.Project, &p.Project.ID) != nil || p.Project.ID == "" {
					return board.ErrInvalidProject
				}
			}
		}

		v, err = h.service.Update(ctx, c.Params("board_id"), p, r)

		return err
	})
	if err != nil {
		return publicError(err)
	}

	etag(c, v.Board.Revision())

	return fx.Success(c, boardView(v))
}

// Document serves the committed schema-1 Yjs state for the read-only cloud editor bootstrap.
func (h *Handler) Document(c fiber.Ctx) error {
	var (
		state   []byte
		version int
	)

	err := h.within(c, func(ctx context.Context) error {
		var err error

		state, version, err = h.service.Document(ctx, c.Params("board_id"))

		return err
	})
	if err != nil {
		return publicError(err)
	}

	c.Set("X-Document-Schema-Version", strconv.Itoa(version))

	return c.Type("octet-stream").Send(state)
}

func (h *Handler) page(ctx context.Context, c fiber.Ctx, prefix, filter string) (board.PageRequest, cursor.Scope, error) {
	actor, err := rlstx.Actor(ctx)
	if err != nil {
		return board.PageRequest{}, cursor.Scope{}, err
	}

	scope := cursor.Scope{ActorID: actor, WorkspaceID: c.Params("workspace_id"), ResourcePrefix: prefix, Filter: filter, Order: "updated_at_desc_id_desc"}

	p := board.PageRequest{Limit: 50}
	if c.Query("limit") != "" {
		p.Limit, err = strconv.Atoi(c.Query("limit"))
		if err != nil || p.Limit < 1 {
			return p, scope, board.ErrInvalidPage
		}
	}

	if c.Query("cursor") != "" {
		position, err := h.cursors.Decode(c.Query("cursor"), scope)
		if err != nil {
			return p, scope, err
		}

		p.After = &board.Position{ID: position.ID, UpdatedAt: position.UpdatedAt}
	}

	return p, scope, p.Validate(prefix)
}

func (h *Handler) next(scope cursor.Scope, p *board.Position) (*string, error) {
	if p == nil {
		return nil, nil
	}

	value, err := h.cursors.Encode(scope, cursor.Position{ID: p.ID, UpdatedAt: p.UpdatedAt})

	return &value, err
}

// ListBoards binds the cursor to actor, workspace and exact project filter.
func (h *Handler) ListBoards(c fiber.Ctx) error {
	items := []boardResponse{}

	var next *string

	err := h.within(c, func(ctx context.Context) error {
		filter := c.Query("project_id")

		var project *string

		if filter != "" {
			v := filter
			if v == "ungrouped" {
				v = ""
			} else if resourceid.Validate(v, board.ProjectIDPrefix) != nil {
				return resourceid.ErrInvalid
			}

			project = &v
		}

		p, scope, err := h.page(ctx, c, board.BoardIDPrefix, filter)
		if err != nil {
			return err
		}

		views, position, err := h.service.List(ctx, c.Params("workspace_id"), project, p)
		if err != nil {
			return err
		}

		for _, v := range views {
			items = append(items, boardView(v))
		}

		next, err = h.next(scope, position)

		return err
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Paginated(c, items, fx.Pagination{NextCursor: next})
}

// ListProjects returns a bounded authorized collection.
func (h *Handler) ListProjects(c fiber.Ctx) error {
	items := []projectResponse{}

	var next *string

	err := h.within(c, func(ctx context.Context) error {
		p, scope, err := h.page(ctx, c, board.ProjectIDPrefix, "")
		if err != nil {
			return err
		}

		page, err := h.service.ListProjects(ctx, c.Params("workspace_id"), p)
		if err != nil {
			return err
		}

		for _, v := range page.Items {
			items = append(items, projectView(v))
		}

		next, err = h.next(scope, page.Next)

		return err
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Paginated(c, items, fx.Pagination{NextCursor: next})
}

// CreateProject accepts only a normalized name and a required idempotency key.
func (h *Handler) CreateProject(c fiber.Ctx) error {
	var p board.Project

	err := h.within(c, func(ctx context.Context) error {
		var body struct {
			Name string `json:"name"`
		}
		if err := bind(c, &body); err != nil {
			return err
		}

		if body.Name != string(board.ProjectName(body.Name).Normalize()) {
			return board.ErrInvalidName
		}

		key, err := key(c)
		if err != nil {
			return err
		}

		p, err = h.service.CreateProject(ctx, c.Params("workspace_id"), body.Name, key)

		return err
	})
	if err != nil {
		return outcome(c, err, "project.create")
	}

	etag(c, p.Revision())

	return fx.Created(c, "/v1/projects/"+p.ID(), projectView(p))
}

// RenameProject applies a strong precondition after resolving the authorized parent.
func (h *Handler) RenameProject(c fiber.Ctx) error {
	var p board.Project

	err := h.within(c, func(ctx context.Context) error {
		r, err := revision(c)
		if err != nil {
			return err
		}

		var body struct {
			Name string `json:"name"`
		}
		if err = bind(c, &body); err != nil {
			return err
		}

		if body.Name != string(board.ProjectName(body.Name).Normalize()) {
			return board.ErrInvalidName
		}

		p, err = h.service.RenameProject(ctx, c.Params("project_id"), body.Name, r)

		return err
	})
	if err != nil {
		return publicError(err)
	}

	etag(c, p.Revision())

	return fx.Success(c, projectView(p))
}

// DeleteProject preserves boards and treats authorized repeated deletion as complete.
func (h *Handler) DeleteProject(c fiber.Ctx) error {
	err := h.within(c, func(ctx context.Context) error {
		r, err := revision(c)
		if err != nil {
			return err
		}

		return h.service.DeleteProject(ctx, c.Params("project_id"), r)
	})
	if err != nil {
		return publicError(err)
	}

	return c.SendStatus(204)
}

// DeleteBoard returns the durable cleanup job after the unavailable state and enqueue commit.
func (h *Handler) DeleteBoard(c fiber.Ctx) error {
	var result any

	err := h.within(c, func(ctx context.Context) error {
		r, err := revision(c)
		if err != nil {
			return err
		}

		result, err = h.service.Delete(ctx, c.Params("board_id"), r)

		return err
	})
	if err != nil {
		return publicError(err)
	}

	c.Status(202)

	return fx.Success(c, result)
}

// SharedBoards returns fresh board projections only for current explicit grants.
func (h *Handler) SharedBoards(c fiber.Ctx) error {
	items := []boardResponse{}

	var next *string

	err := h.within(c, func(ctx context.Context) error {
		actor, err := rlstx.Actor(ctx)
		if err != nil {
			return err
		}

		scope := cursor.Scope{ActorID: actor, ResourcePrefix: board.BoardIDPrefix, Filter: "shared_boards", Order: "id_asc"}

		p := membership.PageRequest{Limit: 50}
		if c.Query("limit") != "" {
			p.Limit, err = strconv.Atoi(c.Query("limit"))
			if err != nil {
				return cursor.ErrInvalid
			}
		}

		if c.Query("cursor") != "" {
			position, err := h.cursors.Decode(c.Query("cursor"), scope)
			if err != nil {
				return err
			}

			p.After = &position
		}

		ids, position, err := h.shared.SharedBoardIDs(ctx, p)
		if err != nil {
			return err
		}

		for _, id := range ids {
			v, err := h.service.Get(ctx, id)
			if err != nil {
				return err
			}

			items = append(items, boardView(v))
		}

		if position != nil {
			value, err := h.cursors.Encode(scope, *position)
			if err != nil {
				return err
			}

			next = &value
		}

		return nil
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Paginated(c, items, fx.Pagination{NextCursor: next})
}
