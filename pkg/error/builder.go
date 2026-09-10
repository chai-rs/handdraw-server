package errx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/samber/lo"
)

/**
 * Builder pattern implementation for creating rich error objects.
 *
 * The builder pattern allows for fluent, chainable error creation with contextual
 * information. This design provides a clean API for building complex error objects
 * while maintaining readability and flexibility.
 *
 * Examples:
 *
 * Basic error creation:
 *   errx.Errorf("Could not fetch users: %w", err)
 *
 * Rich error with context:
 *   errx.
 *     User("steve@apple.com", "firstname", "Samuel").
 *     Tenant("apple", "country", "us").
 *     Errorf("403 not permitted")
 *
 * Error with timing and tracing:
 *   errx.
 *     Time(requestDate).
 *     Duration(requestDuration).
 *     Trace(traceID).
 *     Errorf("Failed to execute http request")
 *
 * Error with custom context:
 *   errx.
 *     With("project_id", project.ID, "created_at", project.CreatedAt).
 *     Errorf("Could not update settings")
 *
 * Thread Safety: ErrorBuilder instances are not thread-safe. Each builder
 * should be used by a single goroutine. The builder methods return new instances
 * to support chaining, which provides some isolation but doesn't guarantee
 * thread safety for concurrent access to the same builder instance.
 */

// ErrorBuilder implements the builder pattern for creating Error instances.
// It provides a fluent API for setting error attributes and creating error objects.
// The builder is designed to be chainable, allowing multiple method calls in sequence.
type ErrorBuilder Error

// newBuilder creates a newBuilder ErrorBuilder with default values.
// This function initializes all fields to their zero values except for time,
// which is set to the current time.
func newBuilder() ErrorBuilder {
	return ErrorBuilder{
		err:      nil,
		msg:      "",
		code:     "",
		status:   http.StatusInternalServerError,
		severity: 0,
		time:     time.Now(),
		duration: 0,

		// context
		domain:  "",
		tags:    []string{},
		context: map[string]any{},

		trace:     "",
		span:      "",
		requestID: "",

		hint:   "",
		public: "",
		owner:  "",

		// user
		userID:     "",
		userData:   map[string]any{},
		tenantID:   "",
		tenantData: map[string]any{},

		// http
		req: nil,
		res: nil,

		// stacktrace
		stacktrace: nil,
	}
}

// copy creates a deep copy of the current builder state.
// This method is used internally to create new builder instances for chaining.
// It performs deep copying of maps to ensure that modifications to the new
// builder don't affect the original.
func (o ErrorBuilder) copy() ErrorBuilder {
	return ErrorBuilder{
		// err:      err,  // Not copied as it's set by error creation methods
		// msg:      o.msg, // Not copied as it's set by error creation methods
		code:     o.code,
		status:   o.status,
		severity: o.severity,
		time:     o.time,
		duration: o.duration,

		domain:  o.domain,
		tags:    o.tags,
		context: lo.Assign(map[string]any{}, o.context), // Deep copy of context map (pointer values are not copied)

		trace:     o.trace,
		span:      o.span,
		requestID: o.requestID,

		hint:   o.hint,
		public: o.public,
		owner:  o.owner,

		userID:     o.userID,
		userData:   lo.Assign(map[string]any{}, o.userData), // Deep copy of user data (pointer values are not copied)
		tenantID:   o.tenantID,
		tenantData: lo.Assign(map[string]any{}, o.tenantData), // Deep copy of tenant data (pointer values are not copied)

		req: o.req,
		res: o.res,

		// stacktrace: o.stacktrace, // Not copied as it's generated per error
	}
}

// Wrap wraps an existing error into an Error with the current builder's context.
// If the input error is nil, returns nil. Otherwise, creates a new Error that
// wraps the original error while preserving all the contextual information set
// in the builder.
//
// Example:
//
//	err := errx.
//	  Code("database_error").
//	  In("database").
//	  Wrap(originalError)
func (o ErrorBuilder) Wrap(err error) error {
	if err == nil {
		return nil
	}

	o2 := o.copy()
	o2.err = err

	if o2.span == "" {
		o2.span = ulid.Make().String() // Generate unique span ID if not set
	}

	o2.stacktrace = newStacktrace(o2.span) // Capture stack trace at error creation

	return Error(o2)
}

// Wrapf wraps an existing error with additional formatted message.
// Similar to Wrap, but adds a formatted message that describes the context
// in which the error occurred.
//
// Example:
//
//	err := errx.
//	  Code("database_error").
//	  In("database").
//	  Wrapf(originalError, "failed to execute query: %s", queryName)
func (o ErrorBuilder) Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}

	o2 := o.copy()
	o2.err = err
	o2.msg = fmt.Errorf(format, args...).Error() // Format the additional message

	if o2.span == "" {
		o2.span = ulid.Make().String()
	}

	o2.stacktrace = newStacktrace(o2.span)

	return Error(o2)
}

// New creates a new error with the specified message.
// This method creates a simple error without wrapping an existing one.
// The message is treated as the primary error message.
//
// Example:
//
//	err := errx.
//	  Code("validation_error").
//	  New("invalid input parameters")
func (o ErrorBuilder) New(message string) error {
	o2 := o.copy()
	o2.err = errors.New(message)

	if o2.span == "" {
		o2.span = ulid.Make().String()
	}

	o2.stacktrace = newStacktrace(o2.span)

	return Error(o2)
}

// Errorf creates a new error with a formatted message.
// Similar to New, but allows for formatted messages using printf-style formatting.
//
// Example:
//
//	err := errx.
//	  Code("validation_error").
//	  Errorf("invalid input: expected %s, got %s", expectedType, actualType)
func (o ErrorBuilder) Errorf(format string, args ...any) error {
	o2 := o.copy()
	o2.err = fmt.Errorf(format, args...)

	if o2.span == "" {
		o2.span = ulid.Make().String()
	}

	o2.stacktrace = newStacktrace(o2.span)

	return Error(o2)
}

// Join combines multiple errors into a single error.
// This method uses the standard errors.Join function to combine multiple
// errors while preserving the builder's context.
//
// Example:
//
//	err := errx.
//	  Code("multi_error").
//	  Join(err1, err2, err3)
func (o ErrorBuilder) Join(e ...error) error {
	return o.Wrap(errors.Join(e...))
}

// Recover handles panics and converts them to Error instances.
// This method executes the provided callback function and catches any panics,
// converting them to properly formatted Error instances with stack traces.
// If the panic payload is already an error, it wraps that error. Otherwise,
// it creates a new error from the panic value.
//
// Example:
//
//	err := errx.
//	  Code("panic_recovered").
//	  Recover(func() {
//	    // Potentially panicking code
//	    riskyOperation()
//	  })
func (o ErrorBuilder) Recover(cb func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = o.Wrap(e) // Wrap existing error
			} else {
				err = o.Wrap(fmt.Errorf("%v", r)) // Convert panic value to error
			}
		}
	}()

	cb()

	return err
}

// Recoverf handles panics with additional context message.
// Similar to Recover, but adds a formatted message to describe the context
// in which the panic occurred.
//
// Example:
//
//	err := errx.
//	  Code("panic_recovered").
//	  Recoverf(func() {
//	    riskyOperation()
//	  }, "panic in operation: %s", operationName)
func (o ErrorBuilder) Recoverf(cb func(), msg string, args ...any) (err error) {
	return o.Wrapf(o.Recover(cb), msg, args...)
}

// Assert panics if the condition is false.
// This method provides a way to add assertions to code that will panic
// with an Error if the condition fails. The assertion can be chained
// with other builder methods.
//
// Example:
//
//	errx.
//	  Code("assertion_failed").
//	  Assert(userID != "", "user ID cannot be empty").
//	  Assert(email != "", "user email cannot be empty").
//	  Assert(orgID != "", "user organization ID cannot be empty")
func (o ErrorBuilder) Assert(condition bool) ErrorBuilder {
	if !condition {
		panic(o.Errorf("assertion failed"))
	}

	return o // Return self for chaining
}

// Assertf panics if the condition is false with a custom message.
// Similar to Assert, but allows for a custom formatted message when
// the assertion fails.
//
// Example:
//
//	errx.
//	  Code("assertion_failed").
//	  Assertf(userID != "", "user ID cannot be empty, got: %s", userID).
//	  Assertf(email != "", "user email cannot be empty, got: %s", email).
//	  Assertf(orgID != "", "user organization ID cannot be empty, got: %s", orgID)
func (o ErrorBuilder) Assertf(condition bool, msg string, args ...any) ErrorBuilder {
	if !condition {
		panic(o.Errorf(msg, args...))
	}

	return o // Return self for chaining
}

// Code sets a machine-readable error code or slug.
// Error codes are useful for programmatic error handling and cross-service
// error correlation. They should be consistent and well-documented.
//
// Example:
//
//	errx.Code("database_connection_failed").Errorf("connection timeout")
func (o ErrorBuilder) Code(code ErrorCode) ErrorBuilder {
	o2 := o.copy()
	o2.code = code

	return o2
}

// Time sets the timestamp when the error occurred.
// If not set, the error will use the current time when created.
//
// Example:
//
//	errx.Time(time.Now()).Errorf("operation failed")
func (o ErrorBuilder) Time(time time.Time) ErrorBuilder {
	o2 := o.copy()
	o2.time = time

	return o2
}

// Since calculates the duration since the specified time.
// This is useful for measuring how long an operation took before failing.
//
// Example:
//
//	start := time.Now()
//	// ... perform operation ...
//	errx.Since(start).Errorf("operation timed out")
func (o ErrorBuilder) Since(t time.Time) ErrorBuilder {
	o2 := o.copy()
	o2.duration = time.Since(t)

	return o2
}

// Duration sets the duration associated with the error.
// This is useful for errors that are related to timeouts or performance issues.
//
// Example:
//
//	errx.Duration(5 * time.Second).Errorf("request timeout")
func (o ErrorBuilder) Duration(duration time.Duration) ErrorBuilder {
	o2 := o.copy()
	o2.duration = duration

	return o2
}

// In sets the domain or feature category for the error.
// Domains help categorize errors by the part of the system they relate to.
//
// Example:
//
//	errx.In("database").Errorf("connection failed")
func (o ErrorBuilder) In(domain string) ErrorBuilder {
	o2 := o.copy()
	o2.domain = domain

	return o2
}

// Tags adds multiple tags for categorizing the error.
// Tags are useful for filtering and grouping errors in monitoring systems.
//
// Example:
//
//	errx.Tags("auth", "permission", "critical").Errorf("access denied")
func (o ErrorBuilder) Tags(tags ...string) ErrorBuilder {
	o2 := o.copy()
	o2.tags = append(o2.tags, tags...)

	return o2
}

// With adds key-value pairs to the error context.
// Context values are useful for debugging and provide additional information
// about the error. Values can be of any type and will be serialized appropriately.
//
// Performance: Context values are stored in a map and processed during error
// creation. Large numbers of context values may impact performance later, but not
// during error creation.
//
// Example:
//
//	errx.With("user_id", 123, "operation", "create").Errorf("validation failed")
func (o ErrorBuilder) With(kv ...any) ErrorBuilder {
	o2 := o.copy()

	// Process key-value pairs in chunks of 2
	for i := 0; i < len(kv); i += 2 {
		if i+1 < len(kv) {
			key, ok := kv[i].(string)
			if ok {
				o2.context[key] = kv[i+1]
			}
		}
	}

	return o2
}

// WithContext extracts values from a Go context and adds them to the error context.
// This is useful for propagating context values through error chains.
//
// Example:
//
//	errx.WithContext(ctx, "request_id", "user_id").Errorf("operation failed")
func (o ErrorBuilder) WithContext(ctx context.Context, keys ...any) ErrorBuilder {
	o2 := o.copy()

	for i := 0; i < len(keys); i++ {
		switch k := keys[i].(type) {
		case fmt.Stringer:
			o2.context[k.String()] = contextValueOrNil(ctx, k.String())
		case string:
			o2.context[k] = contextValueOrNil(ctx, k)
		case *string:
			o2.context[*k] = contextValueOrNil(ctx, *k)
		default:
			o2.context[fmt.Sprint(k)] = contextValueOrNil(ctx, k)
		}
	}

	traceID, spanID := traceIDFromContext(ctx)
	if traceID != "" {
		o2.trace = traceID
	}

	if spanID != "" {
		o2.span = spanID
	}

	return o2
}

// Trace sets a transaction, trace, or correlation ID.
// This is useful for distributed tracing and correlating errors across services.
//
// Example:
//
//	errx.Trace("req-123-456").Errorf("service call failed")
func (o ErrorBuilder) Trace(trace string) ErrorBuilder {
	o2 := o.copy()
	o2.trace = trace

	return o2
}

// Span sets the current span identifier.
// Spans represent units of work and are useful for distributed tracing.
//
// Example:
//
//	errx.Span("database-query").Errorf("query failed")
func (o ErrorBuilder) Span(span string) ErrorBuilder {
	o2 := o.copy()
	o2.span = span

	return o2
}

// Status sets the HTTP status code for the error.
// This is useful for errors that should result in specific HTTP responses.
//
// Example:
//
//	errx.Status(http.StatusNotFound).Errorf("user not found")
func (o ErrorBuilder) Status(status int) ErrorBuilder {
	o2 := o.copy()
	o2.status = status

	return o2
}

// Severity sets the error severity level.
//
// Example:
//
//	errx.WithSeverity(errx.SeverityCritical).Errorf("exchange connection lost")
func (o ErrorBuilder) Severity(severity Severity) ErrorBuilder {
	o2 := o.copy()
	o2.severity = severity

	return o2
}

// RequestID sets the HTTP request ID for correlation.
// This is useful for correlating errors with specific HTTP requests.
//
// Example:
//
//	errx.RequestID("req-123-456").Errorf("request failed")
func (o ErrorBuilder) RequestID(requestID string) ErrorBuilder {
	o2 := o.copy()
	o2.requestID = requestID

	return o2
}

// Hint provides a debugging hint for resolving the error.
// Hints should provide actionable guidance for developers.
//
// Example:
//
//	errx.Hint("Check database connection and credentials").Errorf("connection failed")
func (o ErrorBuilder) Hint(hint string) ErrorBuilder {
	o2 := o.copy()
	o2.hint = hint

	return o2
}

// Public sets a user-safe error message.
// This message should be safe to display to end users without exposing
// internal system details.
//
// Example:
//
//	errx.Public("Unable to process your request").Errorf("internal server error")
func (o ErrorBuilder) Public(public string) ErrorBuilder {
	o2 := o.copy()
	o2.public = public

	return o2
}

// Owner sets the person or team responsible for handling this error.
// This is useful for alerting and error routing.
//
// Example:
//
//	errx.Owner("database-team@company.com").Errorf("connection failed")
func (o ErrorBuilder) Owner(owner string) ErrorBuilder {
	o2 := o.copy()
	o2.owner = owner

	return o2
}

// User adds user information to the error context.
// This method accepts a user ID followed by key-value pairs for user data.
//
// Example:
//
//	errx.User("user-123", "firstname", "John", "lastname", "Doe").Errorf("permission denied")
func (o ErrorBuilder) User(userID string, userData ...any) ErrorBuilder {
	o2 := o.copy()
	o2.userID = userID

	// Process user data key-value pairs
	for i := 0; i < len(userData); i += 2 {
		if i+1 < len(userData) {
			key, ok := userData[i].(string)
			if ok {
				o2.userData[key] = userData[i+1]
			}
		}
	}

	return o2
}

// Tenant adds tenant information to the error context.
// This method accepts a tenant ID followed by key-value pairs for tenant data.
//
// Example:
//
//	errx.Tenant("tenant-456", "name", "Acme Corp", "plan", "premium").Errorf("quota exceeded")
func (o ErrorBuilder) Tenant(tenantID string, tenantData ...any) ErrorBuilder {
	o2 := o.copy()
	o2.tenantID = tenantID

	// Process tenant data key-value pairs
	for i := 0; i < len(tenantData); i += 2 {
		if i+1 < len(tenantData) {
			key, ok := tenantData[i].(string)
			if ok {
				o2.tenantData[key] = tenantData[i+1]
			}
		}
	}

	return o2
}

// Request adds HTTP request information to the error context.
// The withBody parameter controls whether the request body is included.
// Including request bodies may impact performance and memory usage.
//
// Example:
//
//	errx.Request(req, true).Errorf("request processing failed")
func (o ErrorBuilder) Request(req *http.Request, withBody bool) ErrorBuilder {
	o2 := o.copy()
	o2.req = lo.ToPtr(lo.T2(req, withBody))

	return o2
}

// Response adds HTTP response information to the error context.
// The withBody parameter controls whether the response body is included.
// Including response bodies may impact performance and memory usage.
//
// Example:
//
//	errx.Response(res, false).Errorf("response processing failed")
//
//nolint:bodyclose
func (o ErrorBuilder) Response(res *http.Response, withBody bool) ErrorBuilder {
	o2 := o.copy()
	o2.res = lo.ToPtr(lo.T2(res, withBody))

	return o2
}
