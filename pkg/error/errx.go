// Package errx wraps errors with diagnostic context and explicit public outcomes.
package errx

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"time"
)

// Wrap wraps an error into an `errx.Error` object that satisfies `error`.
func Wrap(err error) error {
	if err == nil {
		return nil
	}

	return newBuilder().Wrap(err)
}

// Wrapf wraps an error into an `errx.Error` object that satisfies `error` and formats an error message.
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}

	return newBuilder().Wrapf(err, format, args...)
}

// New returns `errx.Error` object that satisfies `error`.
func New(message string) error {
	return newBuilder().New(message)
}

// Errorf formats an error and returns `errx.Error` object that satisfies `error`.
func Errorf(format string, args ...any) error {
	return newBuilder().Errorf(format, args...)
}

// FromContext returns the ErrorBuilder carried by ctx, or a fresh builder when
// ctx carries none.
func FromContext(ctx context.Context) ErrorBuilder {
	builder, ok := getBuilderFromContext(ctx)
	if !ok {
		return newBuilder()
	}

	return builder
}

// Join combines multiple errors into a single `errx.Error` object that
// satisfies `error`.
func Join(e ...error) error {
	return newBuilder().Join(e...)
}

// Recover handle panic and returns `errx.Error` object that satisfies `error`.
func Recover(cb func()) (err error) {
	return newBuilder().Recover(cb)
}

// Recoverf handle panic and returns `errx.Error` object that satisfies `error` and formats an error message.
func Recoverf(cb func(), msg string, args ...any) (err error) {
	return newBuilder().Recoverf(cb, msg, args...)
}

// Assert panics if condition is false. Panic payload will be of type errx.Error.
// Assertions can be chained.
func Assert(condition bool) ErrorBuilder {
	return newBuilder().Assert(condition)
}

// Assertf panics if condition is false. Panic payload will be of type errx.Error.
// Assertions can be chained.
func Assertf(condition bool, msg string, args ...any) ErrorBuilder {
	return newBuilder().Assertf(condition, msg, args...)
}

// Code set a code or slug that describes the error.
// Error messages are intended to be read by humans, but such code is expected to
// be read by machines and even transported over different services.
func Code(code ErrorCode) ErrorBuilder {
	return newBuilder().Code(code)
}

// Time set the error time.
// Default: `time.Now()`.
func Time(time time.Time) ErrorBuilder {
	return newBuilder().Time(time)
}

// Since set the error duration.
func Since(time time.Time) ErrorBuilder {
	return newBuilder().Since(time)
}

// Duration set the error duration.
func Duration(duration time.Duration) ErrorBuilder {
	return newBuilder().Duration(duration)
}

// In set the feature category or domain.
func In(domain string) ErrorBuilder {
	return newBuilder().In(domain)
}

// Tags adds multiple tags, describing the feature returning an error.
func Tags(tags ...string) ErrorBuilder {
	return newBuilder().Tags(tags...)
}

// Trace set a transaction id, trace id or correlation id...
func Trace(trace string) ErrorBuilder {
	return newBuilder().Trace(trace)
}

// Span represents a unit of work or operation.
func Span(span string) ErrorBuilder {
	return newBuilder().Span(span)
}

// WithSeverity starts a builder with the given severity level set.
//
// Top-level shortcut for newBuilder().Severity(severity); the type
// `Severity` occupies this name at the package level so the entrypoint
// is renamed.
func WithSeverity(severity Severity) ErrorBuilder {
	return newBuilder().Severity(severity)
}

// Status sets the HTTP status code for the error.
func Status(status int) ErrorBuilder {
	return newBuilder().Status(status)
}

// RequestID sets the HTTP request ID for correlation.
func RequestID(requestID string) ErrorBuilder {
	return newBuilder().RequestID(requestID)
}

// With supplies a list of attributes declared by pair of key+value.
func With(kv ...any) ErrorBuilder {
	return newBuilder().With(kv...)
}

// WithContext supplies a list of values declared in context.
func WithContext(ctx context.Context, keys ...any) ErrorBuilder {
	return newBuilder().WithContext(ctx, keys...)
}

// Hint set a hint for faster debugging.
func Hint(hint string) ErrorBuilder {
	return newBuilder().Hint(hint)
}

// Public sets a message that is safe to show to an end user.
func Public(public string) ErrorBuilder {
	return newBuilder().Public(public)
}

// Owner set the name/email of the colleague/team responsible for handling this error.
// Useful for alerting purpose.
func Owner(owner string) ErrorBuilder {
	return newBuilder().Owner(owner)
}

// User supplies a user ID and copies the metadata map into the builder.
func User(userID string, data map[string]any) ErrorBuilder {
	builder := newBuilder().User(userID)
	maps.Copy(builder.userData, data)

	return builder
}

// Tenant supplies a tenant ID and copies the metadata map into the builder.
func Tenant(tenantID string, data map[string]any) ErrorBuilder {
	builder := newBuilder().Tenant(tenantID)
	maps.Copy(builder.tenantData, data)

	return builder
}

// Request supplies a http.Request.
func Request(req *http.Request, withBody bool) ErrorBuilder {
	return newBuilder().Request(req, withBody)
}

// Response supplies a http.Response.
func Response(res *http.Response, withBody bool) ErrorBuilder {
	return newBuilder().Response(res, withBody)
}

// GetPublic returns a message that is safe to show to an end user, or a default generic message.
func GetPublic(err error, defaultPublicMessage string) string {
	var errxError Error

	if errors.As(err, &errxError) {
		msg := errxError.Public()
		if len(msg) > 0 {
			return msg
		}
	}

	return defaultPublicMessage
}
