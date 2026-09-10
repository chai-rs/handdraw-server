// Package api adapts board-domain outcomes to the Handdraw HTTP contract.
package api

import (
	"errors"
	"net/http"

	"github.com/chai-rs/handdraw-server/internal/board/model"
	errx "github.com/chai-rs/handdraw-server/pkg/error"
	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	"github.com/gofiber/fiber/v3"
)

// Handle maps domain errors for Fiber routes mounted by an authorized application workflow.
// It supplies no authentication or routes of its own.
func Handle(handler fiber.Handler) fiber.Handler {
	return func(c fiber.Ctx) error { return Error(handler(c)) }
}

// Error preserves the original cause while attaching a safe public HTTP outcome.
func Error(err error) error {
	if err == nil {
		return nil
	}

	status, code, message := http.StatusInternalServerError, "internal_error", "An internal error occurred"

	switch {
	case errors.Is(err, resourceid.ErrInvalid):
		status, code, message = 400, "invalid_resource_id", "Invalid resource identifier"
	case errors.Is(err, model.ErrNotFound):
		status, code, message = 404, "not_found", "Resource not found"
	case errors.Is(err, model.ErrPermissionDenied):
		status, code, message = 403, "permission_denied", "Permission denied"
	case errors.Is(err, model.ErrRevisionConflict):
		status, code, message = 412, "revision_conflict", "Reload the resource before editing"
	case errors.Is(err, model.ErrProjectNotEmpty):
		status, code, message = 409, "project_not_empty", "Move or delete the project's boards first"
	case errors.Is(err, model.ErrInvalidProject):
		status, code, message = 400, "invalid_request", "Invalid project assignment"
	case errors.Is(err, model.ErrInvalidName), errors.Is(err, model.ErrInvalidPage), errors.Is(err, model.ErrInvalidRevision), errors.Is(err, model.ErrInvalidState):
		status, code, message = 400, "invalid_request", "Invalid request"
	default:
		var transport *fiber.Error
		if errors.As(err, &transport) {
			return err
		}
	}

	return errx.Status(status).Code(errx.ErrorCode(code)).Public(message).Wrap(err)
}
