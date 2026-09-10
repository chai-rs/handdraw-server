package logx

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Event is a deferred log event. Fields are collected and applied when a level
// dispatch method is called, allowing level selection after field construction.
type Event struct {
	appliers []func(*zerolog.Event) *zerolog.Event
}

// NewEvent creates a new deferred event with no fields set.
func NewEvent() *Event { return &Event{} }

func (e *Event) add(fn func(*zerolog.Event) *zerolog.Event) *Event {
	e.appliers = append(e.appliers, fn)
	return e
}

func (e *Event) apply(ev *zerolog.Event) *zerolog.Event {
	for _, fn := range e.appliers {
		ev = fn(ev)
	}

	return ev
}

// Dispatch — level is selected here.

// Debug dispatches the collected fields as a debug-level event.
func (e *Event) Debug(msg string) { e.apply(log.Debug().Caller(2)).Msg(msg) }

// Info dispatches the collected fields as an info-level event.
func (e *Event) Info(msg string) { e.apply(log.Info().Caller(2)).Msg(msg) }

// Warn dispatches the collected fields as a warn-level event.
func (e *Event) Warn(msg string) { e.apply(log.Warn().Caller(2)).Msg(msg) }

// Error dispatches the collected fields as an error-level event.
func (e *Event) Error(msg string) { e.apply(log.Error().Caller(2)).Msg(msg) }

// Panic dispatches the collected fields as a panic-level event, then panics.
func (e *Event) Panic(msg string) { e.apply(log.Panic().Caller(2)).Msg(msg) }

// Fatal dispatches the collected fields as a fatal-level event, then exits.
func (e *Event) Fatal(msg string) { e.apply(log.Fatal().Caller(2)).Msg(msg) }

// Debugf dispatches the collected fields as a formatted debug-level event.
func (e *Event) Debugf(msg string, args ...any) { e.apply(log.Debug().Caller(2)).Msgf(msg, args...) }

// Infof dispatches the collected fields as a formatted info-level event.
func (e *Event) Infof(msg string, args ...any) { e.apply(log.Info().Caller(2)).Msgf(msg, args...) }

// Warnf dispatches the collected fields as a formatted warn-level event.
func (e *Event) Warnf(msg string, args ...any) { e.apply(log.Warn().Caller(2)).Msgf(msg, args...) }

// Errorf dispatches the collected fields as a formatted error-level event.
func (e *Event) Errorf(msg string, args ...any) { e.apply(log.Error().Caller(2)).Msgf(msg, args...) }

// Panicf dispatches the collected fields as a formatted panic-level event, then
// panics.
func (e *Event) Panicf(msg string, args ...any) { e.apply(log.Panic().Caller(2)).Msgf(msg, args...) }

// Fatalf dispatches the collected fields as a formatted fatal-level event, then
// exits.
func (e *Event) Fatalf(msg string, args ...any) { e.apply(log.Fatal().Caller(2)).Msgf(msg, args...) }

// Send dispatches with a programmatic level.
func (e *Event) Send(level zerolog.Level, msg string) {
	e.apply(log.WithLevel(level)).Msg(msg)
}

// String fields

// Str adds a string field to the event.
func (e *Event) Str(key, val string) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Str(key, val) })
}

// Strs adds a string slice field to the event.
func (e *Event) Strs(key string, vals []string) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Strs(key, vals) })
}

// Stringer adds a field rendered via val.String() to the event.
func (e *Event) Stringer(key string, val fmt.Stringer) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Stringer(key, val) })
}

// Bytes adds a byte slice field to the event, rendered as a string.
func (e *Event) Bytes(key string, val []byte) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Bytes(key, val) })
}

// Hex adds a byte slice field to the event, rendered as hexadecimal.
func (e *Event) Hex(key string, val []byte) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Hex(key, val) })
}

// RawJSON adds an already-encoded JSON field to the event without re-encoding.
func (e *Event) RawJSON(key string, b []byte) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.RawJSON(key, b) })
}

// Error fields

// Err adds err to the event under zerolog's default error key.
func (e *Event) Err(err error) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Err(err) })
}

// AnErr adds err to the event under the given key.
func (e *Event) AnErr(key string, err error) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.AnErr(key, err) })
}

// Errs adds a slice of errors to the event under the given key.
func (e *Event) Errs(key string, errs []error) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Errs(key, errs) })
}

// Stack enables stack trace rendering for errors attached to the event.
func (e *Event) Stack() *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Stack() })
}

// Bool fields

// Bool adds a bool field to the event.
func (e *Event) Bool(key string, b bool) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Bool(key, b) })
}

// Bools adds a bool slice field to the event.
func (e *Event) Bools(key string, b []bool) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Bools(key, b) })
}

// Int fields

// Int adds an int field to the event.
func (e *Event) Int(key string, i int) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Int(key, i) })
}

// Ints adds an int slice field to the event.
func (e *Event) Ints(key string, i []int) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Ints(key, i) })
}

// Int8 adds an int8 field to the event.
func (e *Event) Int8(key string, i int8) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Int8(key, i) })
}

// Int16 adds an int16 field to the event.
func (e *Event) Int16(key string, i int16) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Int16(key, i) })
}

// Int32 adds an int32 field to the event.
func (e *Event) Int32(key string, i int32) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Int32(key, i) })
}

// Int64 adds an int64 field to the event.
func (e *Event) Int64(key string, i int64) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Int64(key, i) })
}

// Uint fields

// Uint adds a uint field to the event.
func (e *Event) Uint(key string, i uint) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Uint(key, i) })
}

// Uint8 adds a uint8 field to the event.
func (e *Event) Uint8(key string, i uint8) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Uint8(key, i) })
}

// Uint16 adds a uint16 field to the event.
func (e *Event) Uint16(key string, i uint16) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Uint16(key, i) })
}

// Uint32 adds a uint32 field to the event.
func (e *Event) Uint32(key string, i uint32) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Uint32(key, i) })
}

// Uint64 adds a uint64 field to the event.
func (e *Event) Uint64(key string, i uint64) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Uint64(key, i) })
}

// Float fields

// Float32 adds a float32 field to the event.
func (e *Event) Float32(key string, f float32) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Float32(key, f) })
}

// Float64 adds a float64 field to the event.
func (e *Event) Float64(key string, f float64) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Float64(key, f) })
}

// Time fields

// Time adds a time.Time field to the event, formatted with zerolog's time
// format.
func (e *Event) Time(key string, t time.Time) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Time(key, t) })
}

// Times adds a time.Time slice field to the event.
func (e *Event) Times(key string, t []time.Time) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Times(key, t) })
}

// Dur adds a time.Duration field to the event.
func (e *Event) Dur(key string, d time.Duration) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Dur(key, d) })
}

// Durs adds a time.Duration slice field to the event.
func (e *Event) Durs(key string, d []time.Duration) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Durs(key, d) })
}

// TimeDiff adds a field holding the duration between t and start.
func (e *Event) TimeDiff(key string, t, start time.Time) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.TimeDiff(key, t, start) })
}

// Timestamp adds the current time to the event under zerolog's timestamp key.
func (e *Event) Timestamp() *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Timestamp() })
}

// Generic fields

// Any adds a field of arbitrary type to the event, encoded by reflection.
func (e *Event) Any(key string, i any) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Any(key, i) })
}

// Fields adds a set of fields supplied as a map or as an alternating key/value
// slice.
func (e *Event) Fields(fields any) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Fields(fields) })
}

// Ctx attaches ctx to the event so zerolog hooks can read from it.
func (e *Event) Ctx(ctx context.Context) *Event {
	return e.add(func(ev *zerolog.Event) *zerolog.Event { return ev.Ctx(ctx) })
}
