// Package logger bridges errx errors into zerolog's error marshalling hooks.
// It lives in its own package so that errx itself stays free of any logging
// dependency.
package logger

import (
	errx "github.com/chai-rs/handdraw-server/pkg/error"
)

// StackMarshaller is a zerolog.ErrorStackMarshaler that renders the stack trace
// captured by errx. It returns nil for errors that carry no errx stack, which
// tells zerolog to omit the stack field.
//
// Example:
//
//	zerolog.ErrorStackMarshaler = logger.StackMarshaller
func StackMarshaller(err error) any {
	e, ok := errx.AsError(err)
	if !ok {
		return nil
	}

	stacktrace := e.Stacktrace()
	if stacktrace == "" {
		return nil
	}

	return stacktrace
}

// MarshalFunc is a zerolog.ErrorMarshalFunc that renders an errx error as its
// full structured payload. Plain errors are passed through untouched so zerolog
// falls back to their message.
//
// The stack trace is stripped from the payload because StackMarshaller already
// emits it under zerolog's own stack field; without that, every logged errx
// error would carry two copies of the same trace.
//
// Example:
//
//	zerolog.ErrorMarshalFunc = logger.MarshalFunc
func MarshalFunc(err error) any {
	e, ok := errx.AsError(err)
	if !ok {
		return err
	}

	payload := e.ToMap()
	delete(payload, "stacktrace")

	return payload
}
