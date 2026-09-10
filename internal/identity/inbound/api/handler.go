// Package api exposes identity endpoints through the shared Fiber response contract.
package api

import (
	"errors"
	"strings"

	"github.com/chai-rs/handdraw-server/internal/identity/inbound/api/dto"
	"github.com/chai-rs/handdraw-server/internal/identity/model"
	"github.com/chai-rs/handdraw-server/internal/identity/service"
	errx "github.com/chai-rs/handdraw-server/pkg/error"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/gofiber/fiber/v3"
)

// Handler binds identity routes without depending on another domain.
type Handler struct{ identity *service.Service }

// New binds the identity service to its inbound adapter.
func New(identity *service.Service) *Handler { return &Handler{identity: identity} }

// Register mounts the current-user endpoint on the application's versioned router.
func (h *Handler) Register(router fiber.Router) { router.Get("/me", h.Me) }

// Me authenticates one bearer token and returns only the current user's public fields.
func (h *Handler) Me(c fiber.Ctx) error {
	headers := c.Request().Header.PeekAll("Authorization")
	if len(headers) != 1 {
		return errx.Status(401).Code("unauthenticated").Public("Authentication required").Wrap(model.ErrUnauthenticated)
	}

	parts := strings.Fields(string(headers[0]))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return errx.Status(401).Code("unauthenticated").Public("Authentication required").Wrap(model.ErrUnauthenticated)
	}

	principal, err := h.identity.Authenticate(c.Context(), model.AccessToken(parts[1]))
	if errors.Is(err, model.ErrUnauthenticated) {
		return errx.Status(401).Code("unauthenticated").Public("Authentication required").Wrap(err)
	}

	if errors.Is(err, model.ErrUnavailable) {
		return errx.Status(503).Code("identity_unavailable").Public("Identity service unavailable").Wrap(err)
	}

	if err != nil {
		return err
	}

	return fx.Success(c, dto.Me{ID: principal.Profile.ID(), DisplayName: principal.Profile.DisplayName(), Email: principal.Identity.Email})
}
