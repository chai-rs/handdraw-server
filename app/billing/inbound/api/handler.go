// Package api exposes reviewed billing intentions, never a payment-settlement shortcut.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	"github.com/chai-rs/handdraw-server/app/billing/service"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/gofiber/fiber/v3"
)

// Session authenticates and scopes one request transaction.
//
//mockery:generate: true
type Session interface {
	Run(context.Context, identity.AccessToken, func(context.Context) error) error
}

// Handler exposes the deterministic adapter's available billing capabilities.
type Handler struct {
	session Session
	service *service.Service
}

// New binds the authenticated request boundary.
func New(session Session, service *service.Service) *Handler {
	return &Handler{session: session, service: service}
}

// Register groups subscription, order history and reviewed intentions under the workspace.
func (h *Handler) Register(r fiber.Router) {
	base := "/workspaces/:workspace_id"
	r.Get(base+"/subscription", h.handle("subscription"))
	r.Get(base+"/billing/history", h.handle("history"))
	r.Get(base+"/billing/plan-changes/:intent_id", h.handle("status"))
	r.Post(base+"/billing/plan-change-quotes", h.handle("quote"))
	r.Post(base+"/billing/seat-change-quotes", h.handle("quote"))
	r.Post(base+"/billing/plan-changes", h.handle("confirm"))
	r.Post(base+"/billing/seat-changes", h.handle("confirm"))
	r.Post(base+"/billing/checkouts", h.handle("checkout"))
	r.Post(base+"/billing/cancellation", h.handle("cancel"))
	r.Delete(base+"/billing/cancellation", h.handle("resume"))
}

func publicError(err error) error {
	status, code := 503, "dependency_unavailable"

	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		status, code = 401, "unauthenticated"
	case errors.Is(err, model.ErrInvalid):
		status, code = 400, "invalid_request"
	case errors.Is(err, model.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, model.ErrDenied):
		status, code = 403, "permission_denied"
	case errors.Is(err, model.ErrConflict):
		status, code = 409, "billing_conflict"
	}

	return fx.RequestError(status, code, strings.ReplaceAll(code, "_", " "), err)
}

func (h *Handler) handle(action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		var command model.Command

		if c.Method() == "POST" || c.Method() == "DELETE" && len(c.Body()) > 0 {
			if !strings.HasPrefix(c.Get("Content-Type"), "application/json") || len(c.Body()) > 4096 {
				return publicError(model.ErrInvalid)
			}

			d := json.NewDecoder(bytes.NewReader(c.Body()))
			d.DisallowUnknownFields()

			if d.Decode(&command) != nil {
				return publicError(model.ErrInvalid)
			}

			var extra any
			if d.Decode(&extra) != io.EOF {
				return publicError(model.ErrInvalid)
			}
		}

		if action == "status" {
			command.QuoteID = c.Params("intent_id")
		}

		all := c.Request().Header.PeekAll("Authorization")
		if len(all) != 1 {
			return publicError(identity.ErrUnauthenticated)
		}

		fields := strings.Fields(string(all[0]))
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			return publicError(identity.ErrUnauthenticated)
		}

		var result json.RawMessage

		err := h.session.Run(c.Context(), identity.AccessToken(fields[1]), func(ctx context.Context) error {
			var e error

			result, e = h.service.Request(ctx, c.Params("workspace_id"), action, c.Get("Idempotency-Key"), command)

			return e
		})
		if err != nil {
			return publicError(err)
		}

		c.Set("Cache-Control", "no-store")

		if action == "confirm" || action == "checkout" || action == "cancel" || action == "resume" {
			c.Status(202)
		}

		return fx.Success(c, result)
	}
}
