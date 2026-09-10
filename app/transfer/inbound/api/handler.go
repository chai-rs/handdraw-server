// Package api enqueues asynchronous transfers and serves their authorized status.
package api

import (
	"context"
	"errors"
	"strings"

	"github.com/chai-rs/handdraw-server/app/transfer/model"
	"github.com/chai-rs/handdraw-server/app/transfer/service"
	asset "github.com/chai-rs/handdraw-server/internal/asset/model"
	idem "github.com/chai-rs/handdraw-server/internal/idempotency/model"
	identity "github.com/chai-rs/handdraw-server/internal/identity/model"
	fx "github.com/chai-rs/handdraw-server/pkg/fiber"
	"github.com/gofiber/fiber/v3"
)

// Session verifies identity before an actor-scoped request transaction.
//
//mockery:generate: true
type Session interface {
	Run(context.Context, identity.AccessToken, func(context.Context) error) error
}

// Handler never treats redirects or queue admission as successful document publication.
type Handler struct {
	session Session
	service *service.Service
}

// New binds only request-side workflow dependencies.
func New(session Session, service *service.Service) *Handler {
	return &Handler{session: session, service: service}
}

// Register installs import/export/job-status routes under /v1.
func (h *Handler) Register(r fiber.Router) {
	r.Post("/boards/:board_id/imports", h.create)
	r.Post("/boards/:board_id/exports", h.create)
	r.Get("/jobs/:job_id", h.get)
	r.Get("/jobs/:job_id/download", h.download)
}

func (h *Handler) within(c fiber.Ctx, fn func(context.Context) error) error {
	all := c.Request().Header.PeekAll("Authorization")
	if len(all) != 1 {
		return identity.ErrUnauthenticated
	}

	fields := strings.Fields(string(all[0]))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return identity.ErrUnauthenticated
	}

	return h.session.Run(c.Context(), identity.AccessToken(fields[1]), fn)
}

func publicError(err error) error {
	status, code := 503, "dependency_unavailable"

	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		status, code = 401, "unauthenticated"
	case errors.Is(err, model.ErrNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, model.ErrDenied):
		status, code = 403, "permission_denied"
	case errors.Is(err, model.ErrInvalid), errors.Is(err, idem.ErrInvalid):
		status, code = 400, "invalid_request"
	case errors.Is(err, model.ErrConflict), errors.Is(err, idem.ErrConflict), errors.Is(err, asset.ErrQuota):
		status, code = 409, "transfer_conflict"
	}

	return fx.RequestError(status, code, strings.ReplaceAll(code, "_", " "), err)
}

func (h *Handler) create(c fiber.Ctx) error {
	if !strings.HasPrefix(c.Get("Content-Type"), "application/json") || len(c.Body()) > 4096 {
		return publicError(model.ErrInvalid)
	}

	var job model.Job

	err := h.within(c, func(ctx context.Context) error {
		var err error

		if strings.HasSuffix(c.Path(), "/imports") {
			var p model.Import
			if c.Bind().Body(&p) != nil {
				return model.ErrInvalid
			}

			job, err = h.service.Import(ctx, c.Params("board_id"), c.Get("Idempotency-Key"), p)
		} else {
			var p model.Export
			if c.Bind().Body(&p) != nil {
				return model.ErrInvalid
			}

			job, err = h.service.Export(ctx, c.Params("board_id"), c.Get("Idempotency-Key"), p)
		}

		return err
	})
	if err != nil {
		return publicError(err)
	}

	c.Set("Location", "/v1/jobs/"+job.ID)
	c.Status(202)

	return fx.Success(c, job)
}

func (h *Handler) get(c fiber.Ctx) error {
	var job model.Job

	err := h.within(c, func(ctx context.Context) error {
		var e error

		job, e = h.service.Get(ctx, c.Params("job_id"))

		return e
	})
	if err != nil {
		return publicError(err)
	}

	return fx.Success(c, job)
}

func (h *Handler) download(c fiber.Ctx) error {
	var job model.Job

	err := h.within(c, func(ctx context.Context) error {
		var e error

		job, e = h.service.Get(ctx, c.Params("job_id"))

		return e
	})
	if err != nil {
		return publicError(err)
	}

	if job.Status != "succeeded" || job.ResultAssetID == nil {
		return publicError(model.ErrConflict)
	}
	// Return an authenticated API path; browsers must fetch with the current token, never a public S3 URL.
	return fx.Success(c, struct {
		DownloadPath string `json:"download_path"`
	}{DownloadPath: "/v1/assets/" + *job.ResultAssetID + "/download"})
}
