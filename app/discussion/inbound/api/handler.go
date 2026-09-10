// Package api exposes board discussions separately from collaborative note editing.
package api

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/chai-rs/handdraw-server/app/discussion/service"
	comment "github.com/chai-rs/handdraw-server/internal/comment/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	"github.com/chai-rs/handdraw-server/pkg/cursor"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/chai-rs/handdraw-server/pkg/rlstx"
	"github.com/gofiber/fiber/v3"
)

// Session authenticates before starting an actor-scoped transaction.
//
//mockery:generate: true
type Session interface {
	Run(context.Context, identity.AccessToken, func(context.Context) error) error
}

// Handler owns HTTP contracts and scoped pagination.
type Handler struct {
	session Session
	service *service.Service
	cursors *cursor.Codec
}

// New composes the authenticated discussion endpoints.
func New(session Session, service *service.Service, cursors *cursor.Codec) *Handler {
	return &Handler{session: session, service: service, cursors: cursors}
}

// Register installs discussion routes under /v1.
func (h *Handler) Register(r fiber.Router) {
	r.Get("/boards/:board_id/comment-threads", h.threads)
	r.Post("/boards/:board_id/comment-threads", h.create)
	r.Patch("/comment-threads/:thread_id", h.resolve)
	r.Get("/comment-threads/:thread_id/comments", h.comments)
	r.Post("/comment-threads/:thread_id/comments", h.reply)
	r.Patch("/comments/:comment_id", h.edit)
	r.Delete("/comments/:comment_id", h.edit)
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

func bind(c fiber.Ctx, p any) error {
	if !strings.EqualFold(strings.TrimSpace(strings.Split(c.Get("Content-Type"), ";")[0]), "application/json") {
		return fx.RequestError(415, "unsupported_media_type", "Use application/json", nil)
	}

	if c.Bind().Body(p) != nil {
		return comment.ErrInvalid
	}

	return nil
}

func publicError(err error) error {
	status, code := 503, "dependency_unavailable"

	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		status, code = 401, "unauthenticated"
	case errors.Is(err, comment.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, comment.ErrDenied):
		status, code = 403, "permission_denied"
	case errors.Is(err, comment.ErrConflict):
		status, code = 412, "revision_conflict"
	case errors.Is(err, fx.ErrPreconditionRequired):
		status, code = 428, "precondition_required"
	case errors.Is(err, comment.ErrInvalid), errors.Is(err, cursor.ErrInvalid), errors.Is(err, fx.ErrInvalidPrecondition):
		status, code = 400, "invalid_request"
	}

	return fx.RequestError(status, code, strings.ReplaceAll(code, "_", " "), err)
}

func (h *Handler) page(ctx context.Context, c fiber.Ctx, prefix string) (int, string, cursor.Scope, error) {
	actor, err := rlstx.Actor(ctx)

	scope := cursor.Scope{ActorID: actor, ResourcePrefix: prefix, Filter: c.Params("board_id") + ":" + c.Params("thread_id") + ":" + c.Query("status"), Order: "id_asc"}
	if err != nil {
		return 0, "", scope, err
	}

	limit := 50
	if c.Query("limit") != "" {
		limit, err = strconv.Atoi(c.Query("limit"))
		if err != nil || limit < 1 || limit > 100 {
			return 0, "", scope, comment.ErrInvalid
		}
	}

	after := ""

	if c.Query("cursor") != "" {
		p, e := h.cursors.Decode(c.Query("cursor"), scope)
		if e != nil {
			return 0, "", scope, e
		}

		after = p.ID
	}

	return limit, after, scope, nil
}

func (h *Handler) next(scope cursor.Scope, id string, at time.Time) (*string, error) {
	v, e := h.cursors.Encode(scope, cursor.Position{ID: id, UpdatedAt: at})
	return &v, e
}

func (h *Handler) threads(c fiber.Ctx) error {
	var (
		items []comment.Thread
		next  *string
	)

	err := h.within(c, func(ctx context.Context) error {
		limit, after, scope, e := h.page(ctx, c, comment.ThreadIDPrefix)
		if e != nil {
			return e
		}

		items, e = h.service.Threads(ctx, c.Params("board_id"), c.Query("status"), after, limit+1)
		if e != nil {
			return e
		}

		if len(items) > limit {
			items = items[:limit]
			last := items[limit-1]
			next, e = h.next(scope, last.ID, last.CreatedAt)
		}

		return e
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Paginated(c, items, fx.Pagination{NextCursor: next})
}

func (h *Handler) comments(c fiber.Ctx) error {
	var (
		items []comment.Comment
		next  *string
	)

	err := h.within(c, func(ctx context.Context) error {
		limit, after, scope, e := h.page(ctx, c, comment.CommentIDPrefix)
		if e != nil {
			return e
		}

		items, e = h.service.Comments(ctx, c.Params("thread_id"), after, limit+1)
		if e != nil {
			return e
		}

		if len(items) > limit {
			items = items[:limit]
			last := items[limit-1]
			next, e = h.next(scope, last.ID, last.CreatedAt)
		}

		return e
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Paginated(c, items, fx.Pagination{NextCursor: next})
}

func (h *Handler) create(c fiber.Ctx) error {
	var p comment.Create
	if err := bind(c, &p); err != nil {
		return publicError(err)
	}

	var result comment.Thread

	err := h.within(c, func(ctx context.Context) error {
		var e error

		result, e = h.service.Create(ctx, c.Params("board_id"), p)

		return e
	})
	if err != nil {
		return publicError(err)
	}

	c.Set("ETag", strconv.Quote(strconv.FormatInt(result.Revision, 10)))

	return fx.Created(c, "/v1/comment-threads/"+result.ID, result)
}

func (h *Handler) reply(c fiber.Ctx) error {
	var p comment.Body
	if err := bind(c, &p); err != nil {
		return publicError(err)
	}

	var result comment.Comment

	err := h.within(c, func(ctx context.Context) error {
		var e error

		result, e = h.service.Reply(ctx, c.Params("thread_id"), p)

		return e
	})
	if err != nil {
		return publicError(err)
	}

	c.Set("ETag", strconv.Quote(strconv.FormatInt(result.Revision, 10)))

	return fx.Created(c, "/v1/comments/"+result.ID, result)
}

func (h *Handler) resolve(c fiber.Ctx) error {
	var p struct {
		Status string `json:"status"`
	}
	if err := bind(c, &p); err != nil {
		return publicError(err)
	}

	revision, err := fx.ParseIfMatch(c.Get("If-Match"))
	if err != nil {
		return publicError(err)
	}

	var result comment.Thread

	err = h.within(c, func(ctx context.Context) error {
		var e error

		result, e = h.service.Resolve(ctx, c.Params("thread_id"), p.Status, revision)

		return e
	})
	if err != nil {
		return publicError(err)
	}

	c.Set("ETag", strconv.Quote(strconv.FormatInt(result.Revision, 10)))

	return fx.Success(c, result)
}

func (h *Handler) edit(c fiber.Ctx) error {
	var p comment.Body

	remove := c.Method() == fiber.MethodDelete
	if !remove {
		if err := bind(c, &p); err != nil {
			return publicError(err)
		}
	}

	revision, err := fx.ParseIfMatch(c.Get("If-Match"))
	if err != nil {
		return publicError(err)
	}

	var result comment.Comment

	err = h.within(c, func(ctx context.Context) error {
		var e error

		result, e = h.service.Edit(ctx, c.Params("comment_id"), p, remove, revision)

		return e
	})
	if err != nil {
		return publicError(err)
	}

	c.Set("ETag", strconv.Quote(strconv.FormatInt(result.Revision, 10)))

	return fx.Success(c, result)
}
