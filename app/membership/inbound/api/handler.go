// Package api serves membership operations after verified identity and transaction completion.
package api

import (
	"context"
	"errors"
	"strconv"
	"strings"

	access "github.com/chai-rs/handdraw-server/app/access/model"
	"github.com/chai-rs/handdraw-server/app/membership/model"
	"github.com/chai-rs/handdraw-server/app/membership/service"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	workspace "github.com/chai-rs/handdraw-server/internal/workspace/model"
	"github.com/chai-rs/handdraw-server/pkg/cursor"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/gofiber/fiber/v3"
)

// Session authenticates the caller before installing the request actor.
//
//mockery:generate: true
type Session interface {
	Run(context.Context, identity.AccessToken, func(context.Context) error) error
}

// Handler never accepts an actor or verified email from request bodies.
type Handler struct {
	session Session
	service *service.Service
	cursors *cursor.Codec
}

// New binds the workflow and actor-scoped cursor codec.
func New(session Session, service *service.Service, cursors *cursor.Codec) *Handler {
	return &Handler{session: session, service: service, cursors: cursors}
}

// Register exposes Team membership and Viewer-only board invitations.
func (h *Handler) Register(r fiber.Router) {
	r.Get("/workspaces/:workspace_id/members", h.Members)
	r.Patch("/workspaces/:workspace_id/members/:user_id", h.ChangeMember)
	r.Delete("/workspaces/:workspace_id/members/:user_id", h.DeleteMember)
	r.Post("/workspaces/:workspace_id/invitations", h.Invite)
	r.Get("/workspaces/:workspace_id/invitations", h.Invitations)
	r.Post("/boards/:board_id/invitations", h.Invite)
	r.Post("/invitations/accept", h.Accept)
	r.Delete("/invitations/:invitation_id", h.Revoke)
	r.Get("/boards/:board_id/guests", h.Guests)
	r.Delete("/boards/:board_id/guests/:user_id", h.RemoveGuest)
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

	if c.Bind().Body(v) != nil {
		return workspace.ErrInvalid
	}

	return nil
}

func publicError(err error) error {
	if err == nil {
		return nil
	}

	status, code, message := 503, "dependency_unavailable", "Service unavailable"

	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		status, code, message = 401, "unauthenticated", "Authentication required"
	case errors.Is(err, workspace.ErrNotFound), errors.Is(err, access.ErrNotFound):
		status, code, message = 404, "not_found", "Resource not found"
	case errors.Is(err, workspace.ErrForbidden), errors.Is(err, access.ErrDenied):
		status, code, message = 403, "permission_denied", "Permission denied"
	case errors.Is(err, workspace.ErrCapacity):
		status, code, message = 409, "membership_conflict", "Check seat capacity and existing membership or invitation"
	case errors.Is(err, workspace.ErrInvitationGone):
		status, code, message = 410, "invitation_gone", "Invitation has expired or is no longer pending"
	case errors.Is(err, workspace.ErrRevisionConflict):
		status, code, message = 412, "revision_conflict", "Reload the current member revision"
	case errors.Is(err, fx.ErrPreconditionRequired):
		status, code, message = 428, "precondition_required", "If-Match is required"
	case errors.Is(err, idem.ErrConflict):
		status, code, message = 409, "idempotency_conflict", "Key already used for different input"
	case errors.Is(err, errMedia):
		status, code, message = 415, "unsupported_media_type", "Content-Type must be application/json"
	case errors.Is(err, cursor.ErrInvalid):
		status, code, message = 400, "invalid_cursor", "Invalid cursor"
	case errors.Is(err, workspace.ErrInvalid), errors.Is(err, resourceid.ErrInvalid), errors.Is(err, fx.ErrInvalidPrecondition), errors.Is(err, idem.ErrInvalid):
		status, code, message = 400, "invalid_request", "Invalid request"
	}

	return fx.RequestError(status, code, message, err)
}

func (h *Handler) page(ctx context.Context, c fiber.Ctx, prefix, order string) (model.PageRequest, cursor.Scope, error) {
	actor, err := rlstx.Actor(ctx)
	if err != nil {
		return model.PageRequest{}, cursor.Scope{}, err
	}

	scope := cursor.Scope{ActorID: actor, WorkspaceID: c.Params("workspace_id"), ResourcePrefix: prefix, Filter: "membership", Order: order}

	p := model.PageRequest{Limit: 50}
	if value := c.Query("limit"); value != "" {
		p.Limit, err = strconv.Atoi(value)
		if err != nil {
			return p, scope, workspace.ErrInvalid
		}
	}

	if value := c.Query("cursor"); value != "" {
		position, err := h.cursors.Decode(value, scope)
		if err != nil {
			return p, scope, err
		}

		p.After = &position
	}

	return p, scope, p.Validate(prefix)
}

func (h *Handler) next(scope cursor.Scope, p *cursor.Position) (*string, error) {
	if p == nil {
		return nil, nil
	}

	value, err := h.cursors.Encode(scope, *p)

	return &value, err
}

// Members returns a bounded public roster without recipient emails.
func (h *Handler) Members(c fiber.Ctx) error {
	var (
		items []model.Member
		next  *string
	)

	err := h.within(c, func(ctx context.Context) error {
		p, scope, err := h.page(ctx, c, workspace.UserIDPrefix, "id_asc")
		if err != nil {
			return err
		}

		var position *cursor.Position

		items, position, err = h.service.Members(ctx, c.Params("workspace_id"), p)
		if err != nil {
			return err
		}

		next, err = h.next(scope, position)

		return err
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Paginated(c, items, fx.Pagination{NextCursor: next})
}

func memberInput(c fiber.Ctx) (workspace.RoleChange, error) {
	rev, err := fx.ParseIfMatch(c.Get("If-Match"))
	if err != nil {
		return workspace.RoleChange{}, err
	}

	if len(c.Request().Header.PeekAll("If-Match")) != 1 {
		return workspace.RoleChange{}, fx.ErrInvalidPrecondition
	}

	return workspace.RoleChange{WorkspaceID: c.Params("workspace_id"), UserID: c.Params("user_id"), Revision: rev}, nil
}

// ChangeMember checks the strong membership ETag before returning the updated roster entry.
func (h *Handler) ChangeMember(c fiber.Ctx) error {
	var result model.Member

	err := h.within(c, func(ctx context.Context) error {
		p, err := memberInput(c)
		if err != nil {
			return err
		}

		var body struct {
			Role workspace.Role `json:"role"`
		}
		if err = bind(c, &body); err != nil {
			return err
		}

		p.Role = &body.Role
		result, err = h.service.ChangeMember(ctx, p)

		return err
	})
	if err != nil {
		return publicError(err)
	}

	etag, _ := fx.StrongETag(result.Revision)
	c.Set("ETag", etag)

	return fx.Success(c, result)
}

// DeleteMember rejects removing the Owner and is harmless when an authorized retry finds no member.
func (h *Handler) DeleteMember(c fiber.Ctx) error {
	err := h.within(c, func(ctx context.Context) error {
		p, err := memberInput(c)
		if err != nil {
			return err
		}

		_, err = h.service.ChangeMember(ctx, p)

		return err
	})
	if err != nil {
		return publicError(err)
	}

	return c.SendStatus(204)
}

// Invite persists recipient-bound pending work; it never claims that an email was delivered.
func (h *Handler) Invite(c fiber.Ctx) error {
	var invitation workspace.Invitation

	err := h.within(c, func(ctx context.Context) error {
		var body struct {
			Email string         `json:"email"`
			Role  workspace.Role `json:"role"`
		}
		if err := bind(c, &body); err != nil {
			return err
		}

		if len(c.Request().Header.PeekAll("Idempotency-Key")) != 1 {
			return idem.ErrInvalid
		}

		var err error

		invitation, err = h.service.Invite(ctx, access.Target{WorkspaceID: c.Params("workspace_id"), BoardID: c.Params("board_id")}, workspace.InviteParams{Email: body.Email, Role: body.Role}, c.Get("Idempotency-Key"))

		return err
	})
	if errors.Is(err, idem.ErrProcessing) {
		c.Status(202)
		return fx.Success(c, fiber.Map{"operation": "invitation.create", "status": "processing"})
	}

	if err != nil {
		return publicError(err)
	}

	c.Status(202)

	return fx.Success(c, fiber.Map{"invitation": invitation, "delivery_status": "pending_integration"})
}

// Invitations reads history with an authenticated keyset cursor and calculated expiry.
func (h *Handler) Invitations(c fiber.Ctx) error {
	var (
		items []workspace.Invitation
		next  *string
	)

	err := h.within(c, func(ctx context.Context) error {
		p, scope, err := h.page(ctx, c, workspace.InvitationIDPrefix, "created_at_desc_id_desc")
		if err != nil {
			return err
		}

		var position *cursor.Position

		items, position, err = h.service.Invitations(ctx, c.Params("workspace_id"), p)
		if err != nil {
			return err
		}

		next, err = h.next(scope, position)

		return err
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Paginated(c, items, fx.Pagination{NextCursor: next})
}

// Accept consumes a single-use token submitted in the body, never via URL or a GET side effect.
func (h *Handler) Accept(c fiber.Ctx) error {
	var result model.AccessResult

	err := h.within(c, func(ctx context.Context) error {
		var body struct {
			Token workspace.InvitationToken `json:"token"`
		}
		if err := bind(c, &body); err != nil {
			return err
		}

		var err error

		result, err = h.service.Accept(ctx, body.Token)

		return err
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Success(c, result)
}

// Revoke cancels pending invitations without undoing accepted membership.
func (h *Handler) Revoke(c fiber.Ctx) error {
	err := h.within(c, func(ctx context.Context) error { return h.service.Revoke(ctx, c.Params("invitation_id")) })
	if err != nil {
		return publicError(err)
	}

	return c.SendStatus(204)
}

// Guests lists the explicit board Viewer grants visible only to its Owner.
func (h *Handler) Guests(c fiber.Ctx) error {
	var result []model.Guest

	err := h.within(c, func(ctx context.Context) error {
		var err error

		result, err = h.service.Guests(ctx, c.Params("board_id"))

		return err
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Success(c, result)
}

// RemoveGuest returns any remaining workspace membership after revocation commits.
func (h *Handler) RemoveGuest(c fiber.Ctx) error {
	var result model.Removal

	err := h.within(c, func(ctx context.Context) error {
		var err error

		result, err = h.service.RemoveGuest(ctx, c.Params("board_id"), c.Params("user_id"))

		return err
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Success(c, result)
}
