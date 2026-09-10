package errx

import (
	"errors"
	"fmt"
	"net/http"
)

// ============================================================================
// Authority Error Constants
// ============================================================================
// These provide backward compatibility with the old pkg/errors authority constants.

var (
	// ErrWorkspaceAuthorityNotFound is returned when workspace authority is not found for user.
	ErrWorkspaceAuthorityNotFound = M(http.StatusForbidden, "workspace authority not found")
	// ErrActionForbidden is returned when an action is not allowed.
	ErrActionForbidden = M(http.StatusForbidden, "action forbidden")
)

// NotFound checks if an error represents a 404 Not Found error.
// This is a backward-compatible helper for error checking.
//
// Example:
//
//	if errx.NotFound(err) {
//	  // Handle not found case
//	}
func NotFound(err error) bool {
	if err == nil {
		return false
	}

	if e, ok := AsError(err); ok {
		return e.Status() == http.StatusNotFound
	}

	return false
}

// AsError checks if an error is an errx.Error instance and returns it if so.
// This function is an alias to errors.As and provides a convenient way to
// type-assert errors to errx.Error without importing the errors package.
//
// This function is useful when you need to access the rich metadata and
// context information stored in errx.Error instances, such as error
// codes, stacktraces, user information, or custom context data.
//
// Example usage:
//
//	err := someFunction()
//	if oopsErr, ok := errx.AsError(err); ok {
//	  // Access oops-specific information
//	  fmt.Printf("Error code: %v\n", oopsErr.Code())
//	  fmt.Printf("Domain: %s\n", oopsErr.Domain())
//	  fmt.Printf("Stacktrace: %s\n", oopsErr.Stacktrace())
//
//	  // Check for specific tags
//	  if oopsErr.HasTag("critical") {
//	    // Handle critical errors differently
//	    sendAlert(oopsErr)
//	  }
//	}
//
//	// Chain with other error handling
//	if oopsErr, ok := errx.AsError(err); ok && oopsErr.Code() == "database_error" {
//	  // Handle database errors specifically
//	  retryOperation()
//	}
func AsError(err error) (Error, bool) {
	var e Error

	ok := errors.As(err, &e)

	return e, ok
}

// ============================================================================
// Backward Compatibility Helpers
// ============================================================================
// These functions provide backward compatibility with the old pkg/errors API.
// They wrap the new builder-pattern API to maintain the same function signatures.

// M creates a new error with the specified HTTP status code and message.
// This is a backward-compatible helper function that wraps the builder pattern.
//
// Example:
//
//	errx.M(http.StatusBadRequest, "invalid input")
func M(code int, message string) error {
	return Status(code).Public(message).New(message)
}

// W wraps err with the specified HTTP status code and an optional public
// message. This is a backward-compatible helper function that wraps the builder
// pattern.
//
// Example:
//
//	errx.W(http.StatusBadGateway, err, "upstream unavailable")
func W(code int, err error, messages ...string) error {
	e := Status(code)

	if len(messages) > 0 {
		e = e.Public(messages[0])
	}

	return e.Wrap(err)
}

// WF wraps err with the specified HTTP status code and a formatted public
// message. This is a backward-compatible helper function that wraps the builder
// pattern.
//
// Example:
//
//	errx.WF(http.StatusBadGateway, err, "upstream %q unavailable", host)
func WF(code int, err error, format string, args ...any) error {
	e := Status(code)

	if format != "" {
		e = e.Public(fmt.Sprintf(format, args...))
	}

	return e.Wrap(err)
}

// E wraps an existing error with an HTTP status code and optional user-facing message.
// This is a backward-compatible helper function that wraps the builder pattern.
//
// Example:
//
//	errx.E(http.StatusNotFound, err, "resource not found")
//	errx.E(http.StatusInternalServerError, err)
func E(code int, err error, messages ...string) error {
	if err == nil {
		return nil
	}

	if len(messages) > 0 {
		return Status(code).Public(messages[0]).Wrap(err)
	}

	return Status(code).Wrap(err)
}

// Internal wraps an error as a 500 Internal Server Error.
// This is a backward-compatible helper function that wraps the builder pattern.
//
// Example:
//
//	errx.Internal(err)
func Internal(err error) error {
	return Wrap(err)
}

// Internalf creates a 500 Internal Server Error with a formatted message.
// This is a backward-compatible helper function that wraps the builder pattern.
//
// Example:
//
//	errx.Internalf("failed to process: %v", err)
func Internalf(format string, args ...any) error {
	return Errorf(format, args...)
}

// F creates a new error with the specified HTTP status code and formatted message.
// This is a backward-compatible helper function that wraps the builder pattern.
//
// Example:
//
//	errx.F(http.StatusBadRequest, "invalid field: %s", fieldName)
func F(code int, format string, args ...any) error {
	return Status(code).Errorf(format, args...)
}
