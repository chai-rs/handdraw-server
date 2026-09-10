// Package api exposes authenticated private uploads and downloads, never public object URLs.
package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/chai-rs/handdraw-server/app/asset_management/service"
	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/gofiber/fiber/v3"
)

// Session verifies identity and installs the transaction actor.
//
//mockery:generate: true
type Session interface {
	Run(context.Context, identity.AccessToken, func(context.Context) error) error
}

// Handler caps concurrent byte operations for the local adapter.
type Handler struct {
	session Session
	service *service.Service
	slots   chan struct{}
}

// New binds authenticated metadata and private byte operations.
func New(session Session, service *service.Service) *Handler {
	return &Handler{session: session, service: service, slots: make(chan struct{}, 5)}
}

// Register installs upload and download endpoints under /v1.
func (h *Handler) Register(r fiber.Router) {
	r.Post("/boards/:board_id/assets", h.reserve)
	r.Put("/assets/:asset_id/upload", h.upload)
	r.Post("/assets/:asset_id/complete", h.complete)
	r.Get("/assets/:asset_id/download", h.download)
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

	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		return errBusy
	}

	return h.session.Run(c.Context(), identity.AccessToken(parts[1]), fn)
}

var errBusy = errors.New("asset slots busy")

func publicError(err error) error {
	status, code := 503, "dependency_unavailable"

	switch {
	case errors.Is(err, errBusy):
		status, code = 429, "busy"
	case errors.Is(err, identity.ErrUnauthenticated):
		status, code = 401, "unauthenticated"
	case errors.Is(err, asset.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, asset.ErrDenied):
		status, code = 403, "permission_denied"
	case errors.Is(err, asset.ErrConflict), errors.Is(err, idem.ErrConflict):
		status, code = 409, "asset_conflict"
	case errors.Is(err, asset.ErrQuota):
		status, code = 409, "storage_quota_exceeded"
	case errors.Is(err, asset.ErrInvalid), errors.Is(err, idem.ErrInvalid):
		status, code = 400, "invalid_request"
	}

	return fx.RequestError(status, code, strings.ReplaceAll(code, "_", " "), err)
}

func (h *Handler) reserve(c fiber.Ctx) error {
	if !strings.HasPrefix(c.Get("Content-Type"), "application/json") {
		return fx.RequestError(415, "unsupported_media_type", "Use application/json", nil)
	}

	var p asset.Reserve
	if len(c.Body()) > 4096 || c.Bind().Body(&p) != nil {
		return publicError(asset.ErrInvalid)
	}

	var a asset.Asset

	err := h.within(c, func(ctx context.Context) error {
		var e error

		a, e = h.service.Reserve(ctx, c.Params("board_id"), c.Get("Idempotency-Key"), p)

		return e
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Created(c, "/v1/assets/"+a.ID, a)
}

func (h *Handler) upload(c fiber.Ctx) error {
	var a asset.Asset

	err := h.within(c, func(ctx context.Context) error {
		var e error

		a, e = h.service.Upload(ctx, c.Params("asset_id"), c.Body())

		return e
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Success(c, a)
}

func (h *Handler) complete(c fiber.Ctx) error {
	if len(c.Body()) != 0 && string(c.Body()) != "{}" {
		return publicError(asset.ErrInvalid)
	}

	var a asset.Asset

	err := h.within(c, func(ctx context.Context) error {
		var e error

		a, e = h.service.Complete(ctx, c.Params("asset_id"))

		return e
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Success(c, a)
}

func (h *Handler) download(c fiber.Ctx) error {
	var (
		a    asset.Asset
		data []byte
	)

	err := h.within(c, func(ctx context.Context) error {
		var e error

		a, data, e = h.service.Download(ctx, c.Params("asset_id"))

		return e
	})
	if err != nil {
		return publicError(err)
	}

	c.Set("Content-Type", a.MIME)
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Content-Disposition", `attachment; filename="`+a.ID+`"`)
	c.Set("Accept-Ranges", "bytes")

	if requested := c.Get("Range"); requested != "" {
		start, end, ok := byteRange(requested, int64(len(data)))
		if !ok {
			c.Set("Content-Range", fmt.Sprintf("bytes */%d", len(data)))
			return c.SendStatus(416)
		}

		c.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))

		return c.Status(206).Send(data[start : end+1])
	}

	return c.Send(data)
}

func byteRange(value string, size int64) (int64, int64, bool) {
	if !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return 0, 0, false
	}

	lo, hi, ok := strings.Cut(strings.TrimPrefix(value, "bytes="), "-")
	if !ok {
		return 0, 0, false
	}

	if lo == "" {
		n, e := strconv.ParseInt(hi, 10, 64)
		if e != nil || n < 1 {
			return 0, 0, false
		}

		return max(0, size-n), size - 1, true
	}

	start, e := strconv.ParseInt(lo, 10, 64)
	if e != nil || start < 0 || start >= size {
		return 0, 0, false
	}

	end := size - 1
	if hi != "" {
		end, e = strconv.ParseInt(hi, 10, 64)
		if e != nil || end < start {
			return 0, 0, false
		}
	}

	return start, min(end, size-1), true
}
