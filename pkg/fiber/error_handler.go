package fx

import (
	"errors"
	"net/http"
	"strings"
	"unicode"

	errx "github.com/chai-rs/handdraw-server/pkg/error"
	logx "github.com/chai-rs/handdraw-server/pkg/logger"
	valx "github.com/chai-rs/handdraw-server/pkg/validator"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
	"github.com/google/uuid"
)

func errorHandler(c fiber.Ctx, err error) error {
	c.Set(fiber.HeaderCacheControl, "private, no-store")

	status := fiber.StatusInternalServerError
	message := "internal server error"
	code := ""

	var details map[string]any

	var bindErr *fiber.BindError
	if errors.As(err, &bindErr) {
		status = fiber.StatusBadRequest
		message = "malformed request"
		code = "malformed_request"
	}

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		status = fiberErr.Code
		if status < fiber.StatusInternalServerError {
			message = fiberErr.Message
		}

		code = statusErrorCode(status)
	}

	if rich, ok := errx.AsError(err); ok {
		if rich.Status() != 0 {
			status = rich.Status()
		}

		if rich.Code() != "" {
			code = string(rich.Code())
		}

		public := rich.Public()
		if status < fiber.StatusInternalServerError && public != "" && public != "An internal server error occurred" {
			message = public
		}

		if invalidFields, invalid := rich.Context()[valx.ErrorKeyInvalidFields]; invalid {
			status = fiber.StatusBadRequest
			message = "invalid request"
			code = "invalid_request"
			details = validationDetails(invalidFields)
		}
	}

	var requestErr *requestError
	if errors.As(err, &requestErr) {
		status = requestErr.status
		code = requestErr.code
		message = requestErr.message
		details = nil
	}

	if code == "" {
		code = statusErrorCode(status)
	}

	requestID := requestid.FromContext(c)
	if requestID == "" {
		requestID = uuid.NewString()
		c.Set(fiber.HeaderXRequestID, requestID)
	}

	logx.Error().
		Str("code", code).
		Str("request_id", requestID).
		Str("method", c.Method()).
		Str("path", c.Path()).
		Int("status", status).
		Msg("request failed")

	publicError := ErrorDetail{Code: code, Message: message, Details: details}

	return c.Status(status).JSON(Response[any]{
		Success: false,
		Meta:    Meta{RequestID: requestID},
		Error:   &publicError,
	})
}

func statusErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthenticated"
	case http.StatusForbidden:
		return "permission_denied"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusRequestTimeout:
		return "request_timeout"
	case http.StatusPreconditionRequired:
		return "precondition_required"
	case http.StatusPreconditionFailed:
		return "revision_conflict"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusUnsupportedMediaType:
		return "unsupported_media_type"
	case http.StatusUnprocessableEntity:
		return "unprocessable_entity"
	case http.StatusTooManyRequests:
		return "too_many_requests"
	case http.StatusNotImplemented:
		return "not_implemented"
	case http.StatusBadGateway:
		return "bad_gateway"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	case http.StatusGatewayTimeout:
		return "gateway_timeout"
	default:
		return "internal_error"
	}
}

func validationDetails(value any) map[string]any {
	invalidFields, ok := value.(map[string]any)
	if !ok || len(invalidFields) == 0 {
		return nil
	}

	fields := make(map[string]any, len(invalidFields))
	for field, message := range invalidFields {
		publicMessage, ok := message.(string)
		if !ok {
			continue
		}

		fields[snakeCase(field)] = publicMessage
	}

	if len(fields) == 0 {
		return nil
	}

	return map[string]any{"fields": fields}
}

func snakeCase(value string) string {
	runes := []rune(value)

	var converted strings.Builder
	converted.Grow(len(value))

	for index, current := range runes {
		if index > 0 && unicode.IsUpper(current) {
			previous := runes[index-1]

			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) ||
				(unicode.IsUpper(previous) && nextIsLower) {
				converted.WriteByte('_')
			}
		}

		converted.WriteRune(unicode.ToLower(current))
	}

	return converted.String()
}
