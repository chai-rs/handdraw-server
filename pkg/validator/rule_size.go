package valx

import (
	"errors"
	"fmt"
	"reflect"
	"unicode/utf8"

	v "github.com/go-ozzo/ozzo-validation/v4"
)

// Size returns a rule constraining the length of a string, slice, array, or
// map to the inclusive range [min, max]. A bound of zero or below is treated as
// unset, so Size(2, 0) enforces only a minimum. Nil values pass; empty
// collections are still checked against min.
func Size(min, max int) v.Rule {
	return v.By(func(value any) error {
		// Use Indirect to unwrap pointers/interfaces and detect nil
		value, isNil := v.Indirect(value)
		if isNil {
			return nil
		}
		// Note: do NOT use v.IsEmpty here — empty collections (len 0)
		// should still be validated against min/max constraints.

		length, err := getLength(value)
		if err != nil {
			return err
		}

		if min > 0 && length < min {
			if max > 0 {
				return fmt.Errorf("must contain between %d and %d items", min, max)
			}

			return fmt.Errorf("must contain at least %d items", min)
		}

		if max > 0 && length > max {
			if min > 0 {
				return fmt.Errorf("must contain between %d and %d items", min, max)
			}

			return fmt.Errorf("must contain at most %d items", max)
		}

		return nil
	})
}

func getLength(value any) (int, error) {
	rv := reflect.ValueOf(value)

	switch rv.Kind() {
	case reflect.String:
		return utf8.RuneCountInString(rv.String()), nil
	case reflect.Slice, reflect.Array:
		return rv.Len(), nil
	case reflect.Map:
		return rv.Len(), nil
	default:
		return 0, errors.New("must be a string, slice, array, or map")
	}
}
