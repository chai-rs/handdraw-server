// Package api exposes authenticated workspace metadata without accepting an actor ID from the client.
package api

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/workspace_management/model"
	"github.com/chai-rs/handdraw-server/app/workspace_management/service"
	billing "github.com/chai-rs/handdraw-server/internal/billing/model"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/cursor"

	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/gofiber/fiber/v3"
)

var errUnsupportedMediaType = errors.New("unsupported media type")

// Handler writes responses only after the authenticated transaction commits.
type Handler struct {
	session    model.Session
	service    *service.Service
	cursors    *cursor.Codec
	onboarding model.Onboarding
}

// New binds application dependencies and a stable server-held cursor signing key.
func New(session model.Session, service *service.Service, cursors *cursor.Codec) *Handler {
	return &Handler{session: session, service: service, cursors: cursors}
}

// Register mounts the T03 metadata subset; onboarding POST remains a separate workflow.
func (h *Handler) Register(router fiber.Router) {
	if h.onboarding != nil {
		router.Post("/workspaces", h.Create)
	}

	router.Get("/workspaces", h.List)
	router.Get("/workspaces/:workspace_id", h.Get)
	router.Patch("/workspaces/:workspace_id", h.Rename)
}

type response struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Kind           string              `json:"kind"`
	Role           workspace.Role      `json:"role"`
	Revision       int64               `json:"revision,string"`
	AccessRevision int64               `json:"access_revision,string"`
	Lifecycle      string              `json:"lifecycle"`
	Capabilities   access.Capabilities `json:"capabilities"`
	Entitlement    billing.Entitlement `json:"entitlement"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

func projection(d access.Decision) response {
	w := d.Facts.Workspace
	return response{ID: w.ID, Name: w.Name, Kind: w.Kind, Role: d.Facts.Member.Role, Revision: w.Revision, AccessRevision: w.AccessRevision, Lifecycle: w.Lifecycle, Capabilities: d.Capabilities, Entitlement: d.Facts.Entitlement, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}
}

func (h *Handler) within(c fiber.Ctx, fn func(context.Context) error) error {
	headers := c.Request().Header.PeekAll("Authorization")
	if len(headers) != 1 {
		return publicError(identity.ErrUnauthenticated)
	}

	parts := strings.Fields(string(headers[0]))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return publicError(identity.ErrUnauthenticated)
	}

	return publicError(h.session.Run(c.Context(), identity.AccessToken(parts[1]), fn))
}

// Get returns current membership, metadata and effective content permissions.
func (h *Handler) Get(c fiber.Ctx) error {
	var d access.Decision

	if err := h.within(c, func(ctx context.Context) error {
		var err error

		d, err = h.service.Get(ctx, c.Params("workspace_id"))

		return err
	}); err != nil {
		return err
	}

	etag, _ := fx.StrongETag(d.Facts.Workspace.Revision)
	c.Set("ETag", etag)

	return fx.Success(c, projection(d))
}

// Rename parses a strict mutation and strong precondition within the verified request scope.
func (h *Handler) Rename(c fiber.Ctx) error {
	var d access.Decision

	if err := h.within(c, func(ctx context.Context) error {
		if !strings.EqualFold(strings.TrimSpace(strings.Split(c.Get("Content-Type"), ";")[0]), "application/json") {
			return errUnsupportedMediaType
		}

		revision, err := fx.ParseIfMatch(c.Get("If-Match"))
		if err != nil {
			return err
		}

		if len(c.Request().Header.PeekAll("If-Match")) != 1 {
			return fx.ErrInvalidPrecondition
		}

		var input struct {
			Name string `json:"name"`
		}
		if err := c.Bind().Body(&input); err != nil {
			return workspace.ErrInvalid
		}

		if workspace.Name(input.Name).Validate() != nil {
			return workspace.ErrInvalid
		}

		d, err = h.service.Rename(ctx, c.Params("workspace_id"), workspace.Name(input.Name), revision)

		return err
	}); err != nil {
		return err
	}

	etag, _ := fx.StrongETag(d.Facts.Workspace.Revision)
	c.Set("ETag", etag)

	return fx.Success(c, projection(d))
}

// List binds an opaque cursor to the verified user rather than caller-supplied identity fields.
func (h *Handler) List(c fiber.Ctx) error {
	items := []response{}

	var next *string

	if err := h.within(c, func(ctx context.Context) error {
		limit := 50

		if raw := c.Query("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return workspace.ErrInvalid
			}

			limit = n
		}

		actor, err := rlstx.Actor(ctx)
		if err != nil {
			return err
		}

		scope := cursor.Scope{ActorID: actor, ResourcePrefix: workspace.WorkspaceIDPrefix, Order: "updated_at_desc_id_desc"}
		p := workspace.PageRequest{Limit: limit}

		if token := c.Query("cursor"); token != "" {
			position, err := h.cursors.Decode(token, scope)
			if err != nil {
				return err
			}

			p.After = &position
		}

		page, err := h.service.List(ctx, p)
		if err != nil {
			return err
		}

		for _, d := range page.Items {
			items = append(items, projection(d))
		}

		if page.Next != nil {
			token, err := h.cursors.Encode(scope, *page.Next)
			if err != nil {
				return err
			}

			next = &token
		}

		return nil
	}); err != nil {
		return err
	}

	return fx.Paginated(c, items, fx.Pagination{NextCursor: next})
}

func publicError(err error) error {
	if err == nil {
		return nil
	}

	status, code, message := 503, "dependency_unavailable", "Service unavailable"

	switch {
	case errors.Is(err, errUnsupportedMediaType):
		status, code, message = 415, "unsupported_media_type", "Content-Type must be application/json"
	case errors.Is(err, identity.ErrUnauthenticated):
		status, code, message = 401, "unauthenticated", "Authentication required"
	case errors.Is(err, access.ErrNotFound), errors.Is(err, workspace.ErrNotFound):
		status, code, message = 404, "not_found", "Workspace not found"
	case errors.Is(err, access.ErrDenied), errors.Is(err, workspace.ErrForbidden):
		status, code, message = 403, "permission_denied", "Permission denied"
	case errors.Is(err, idem.ErrConflict):
		status, code, message = 409, "idempotency_conflict", "Key already used for different input"
	case errors.Is(err, idem.ErrInvalid):
		status, code, message = 400, "invalid_request", "Valid Idempotency-Key is required"
	case errors.Is(err, workspace.ErrRevisionConflict):
		status, code, message = 412, "revision_conflict", "Reload the current workspace revision"
	case errors.Is(err, fx.ErrPreconditionRequired):
		status, code, message = 428, "precondition_required", "If-Match is required"
	case errors.Is(err, cursor.ErrInvalid):
		status, code, message = 400, "invalid_cursor", "Invalid cursor"
	case errors.Is(err, resourceid.ErrInvalid):
		status, code, message = 400, "invalid_resource_id", "Invalid resource ID"
	case errors.Is(err, workspace.ErrInvalid), errors.Is(err, fx.ErrInvalidPrecondition):
		status, code, message = 400, "invalid_request", "Invalid request"
	}

	return fx.RequestError(status, code, message, err)
}

// WithOnboarding enables creation only when its atomic bootstrap workflow is wired.
func (h *Handler) WithOnboarding(onboarding model.Onboarding) *Handler {
	h.onboarding = onboarding
	return h
}

// Create bootstraps an empty workspace without a trial or paid entitlement.
func (h *Handler) Create(c fiber.Ctx) error {
	var d access.Decision

	err := h.within(c, func(ctx context.Context) error {
		if !strings.EqualFold(strings.TrimSpace(strings.Split(c.Get("Content-Type"), ";")[0]), "application/json") {
			return errUnsupportedMediaType
		}

		var input struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		}
		if c.Bind().Body(&input) != nil || workspace.Name(input.Name).Validate() != nil {
			return workspace.ErrInvalid
		}

		if len(c.Request().Header.PeekAll("Idempotency-Key")) != 1 {
			return idem.ErrInvalid
		}

		var err error

		d, err = h.onboarding.Create(ctx, input.Name, input.Kind, c.Get("Idempotency-Key"))

		return err
	})
	if errors.Is(err, idem.ErrProcessing) {
		c.Status(202)
		return fx.Success(c, fiber.Map{"operation": "workspace.create", "status": "processing"})
	}

	if err != nil {
		return err
	}

	etag, _ := fx.StrongETag(d.Facts.Workspace.Revision)
	c.Set("ETag", etag)

	return fx.Created(c, "/v1/workspaces/"+d.Facts.Workspace.ID, projection(d))
}
