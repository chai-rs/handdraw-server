package fx

import (
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/requestid"
)

// Response is the common body returned by application API endpoints.
type Response[T any] struct {
	Success bool         `json:"success"`
	Meta    Meta         `json:"meta"`
	Result  *T           `json:"result,omitempty"`
	Error   *ErrorDetail `json:"error,omitempty"`
}

// Meta contains request-scoped response metadata.
type Meta struct {
	RequestID  string      `json:"request_id,omitempty"`
	Pagination *Pagination `json:"pagination,omitempty"`
}

// Pagination contains Handdraw's opaque keyset cursor inside the shared response metadata.
// A nil cursor serializes as null to mark the end of the collection.
type Pagination struct {
	NextCursor *string `json:"next_cursor"`
}

// ErrorDetail is the public, machine-readable description of a failed request.
type ErrorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Success writes a successful single-result response.
func Success[T any](c fiber.Ctx, result T) error {
	return c.JSON(successResponse(c, result, nil))
}

// Created writes a successful creation response and its resource location.
func Created[T any](c fiber.Ctx, location string, result T) error {
	if location != "" {
		c.Set(fiber.HeaderLocation, location)
	}

	return c.Status(fiber.StatusCreated).JSON(successResponse(c, result, nil))
}

// Paginated writes a successful collection response with paging metadata.
func Paginated[T any](c fiber.Ctx, result []T, pagination Pagination) error {
	if result == nil {
		result = []T{}
	}

	return c.JSON(successResponse(c, result, &pagination))
}

func successResponse[T any](c fiber.Ctx, result T, pagination *Pagination) Response[T] {
	return Response[T]{
		Success: true,
		Meta: Meta{
			RequestID:  requestid.FromContext(c),
			Pagination: pagination,
		},
		Result: &result,
	}
}
