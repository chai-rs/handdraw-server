// Package valx provides shared validation rules and structured field errors.
package valx

import (
	"reflect"

	errx "github.com/chai-rs/handdraw-server/pkg/error"
	logx "github.com/chai-rs/handdraw-server/pkg/logger"
	v "github.com/go-ozzo/ozzo-validation/v4"
)

// ErrorKeyInvalidFields is the error context key under which per-field
// validation messages are attached.
const ErrorKeyInvalidFields = "invalid_fields"

// Must panics through the logger when err is non-nil. The first message, if
// given, replaces the default panic message.
func Must(err error, messages ...string) {
	msg := "validation failed"
	if len(messages) > 0 {
		msg = messages[0]
	}

	if err != nil {
		logx.Panic().Err(err).Msg(msg)
	}
}

// Struct validates the fields of the struct pointed to by structPtr, returning
// the failures wrapped as an errx error.
func Struct(structPtr any, fields ...*v.FieldRules) error {
	err := v.ValidateStruct(structPtr, fields...)
	if err != nil {
		return wrapErr(err)
	}

	return nil
}

// MustStruct is like Struct but panics through the logger on failure.
func MustStruct(structPtr any, fields ...*v.FieldRules) {
	err := Struct(structPtr, fields...)

	Must(err)
}

// Var validates a single value against rules, returning the failure wrapped as
// an errx error.
func Var(value any, rules ...v.Rule) error {
	err := v.Validate(value, rules...)
	if err != nil {
		return wrapErr(err)
	}

	return nil
}

// MustField is like Var but panics through the logger on failure.
func MustField(value any, rules ...v.Rule) {
	err := Var(value, rules...)

	Must(err)
}

// RequireAll validates that every exported field of the struct pointed to by s
// is set. It returns an internal error when s is not a pointer to a struct.
func RequireAll(s any) error {
	elem := reflect.ValueOf(s)

	if elem.Kind() != reflect.Pointer || elem.Elem().Kind() != reflect.Struct {
		return errx.Internalf("expected a pointer of struct, got %T", s)
	}

	rules := make([]*FieldRules, 0, elem.Elem().NumField())
	elem = elem.Elem()
	types := elem.Type()

	for i := range elem.NumField() {
		if !types.Field(i).IsExported() {
			continue
		}

		field := elem.Field(i)
		rules = append(rules, Field(field.Addr().Interface(), v.Required))
	}

	return Struct(s, rules...)
}

// MustRequireAll is like RequireAll but panics through the logger on failure.
func MustRequireAll(structPtr any) {
	Must(RequireAll(structPtr))
}
