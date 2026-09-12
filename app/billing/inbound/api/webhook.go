package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/chai-rs/handdraw-server/app/billing/model"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/gofiber/fiber/v3"
)

// PolarWebhook authenticates and durably records provider events without a user session.
type PolarWebhook struct {
	decoder model.WebhookDecoder
	inbox   model.WebhookInbox
}

// NewPolarWebhook binds the public signature boundary to its function-only inbox.
func NewPolarWebhook(decoder model.WebhookDecoder, inbox model.WebhookInbox) *PolarWebhook {
	return &PolarWebhook{decoder: decoder, inbox: inbox}
}

// Register mounts the public Polar callback outside Bearer authentication.
func (h *PolarWebhook) Register(r fiber.Router) { r.Post("/webhooks/polar", h.handle) }

func (h *PolarWebhook) handle(c fiber.Ctx) error {
	if len(c.Body()) == 0 || len(c.Body()) > 256*1024 {
		return fx.RequestError(http.StatusBadRequest, "invalid_request", "invalid request", model.ErrInvalid)
	}

	event, err := h.decoder.Decode(c.Body(), c.GetReqHeaders())
	if err != nil {
		if errors.Is(err, model.ErrInvalidWebhook) || errors.Is(err, model.ErrInvalid) {
			return fx.RequestError(http.StatusForbidden, "invalid_webhook", "invalid webhook", err)
		}

		return fx.RequestError(http.StatusServiceUnavailable, "dependency_unavailable", "dependency unavailable", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err = h.inbox.Accept(ctx, event); err != nil {
		return fx.RequestError(http.StatusServiceUnavailable, "dependency_unavailable", "dependency unavailable", err)
	}

	return c.SendStatus(http.StatusAccepted)
}
