package valx

import (
	"errors"
	"fmt"
	"time"

	v "github.com/go-ozzo/ozzo-validation/v4"
)

func toTime(value any) (time.Time, error) {
	value, isNil := v.Indirect(value)
	if isNil || v.IsEmpty(value) {
		return time.Time{}, nil
	}

	t, ok := value.(time.Time)
	if !ok {
		return time.Time{}, errors.New("must be a time.Time")
	}

	return t, nil
}

func toTimeFromBoundary(boundary any) (time.Time, bool, error) {
	switch v := boundary.(type) {
	case time.Time:
		return v, false, nil
	case *time.Time:
		if v == nil {
			return time.Time{}, true, nil
		}

		return *v, false, nil
	default:
		return time.Time{}, false, errors.New("boundary must be a time.Time or *time.Time")
	}
}

// TimeGTE validates that a time is greater than or equal to the given minimum.
// The min parameter can be either time.Time or *time.Time.
func TimeGTE(min any) v.Rule {
	return v.By(func(value any) error {
		t, err := toTime(value)
		if err != nil {
			return err
		}

		if t.IsZero() {
			return nil
		}

		minTime, skip, err := toTimeFromBoundary(min)
		if err != nil {
			return err
		}

		if skip {
			return nil
		}

		if t.Before(minTime) {
			return fmt.Errorf("must be no earlier than %s", minTime.Format(time.RFC3339))
		}

		return nil
	})
}

// TimeGT validates that a time is strictly greater than the given minimum.
// The min parameter can be either time.Time or *time.Time.
func TimeGT(min any) v.Rule {
	return v.By(func(value any) error {
		t, err := toTime(value)
		if err != nil {
			return err
		}

		if t.IsZero() {
			return nil
		}

		minTime, skip, err := toTimeFromBoundary(min)
		if err != nil {
			return err
		}

		if skip {
			return nil
		}

		if !t.After(minTime) {
			return fmt.Errorf("must be after %s", minTime.Format(time.RFC3339))
		}

		return nil
	})
}

// TimeLTE validates that a time is less than or equal to the given maximum.
// The max parameter can be either time.Time or *time.Time.
func TimeLTE(max any) v.Rule {
	return v.By(func(value any) error {
		t, err := toTime(value)
		if err != nil {
			return err
		}

		if t.IsZero() {
			return nil
		}

		maxTime, skip, err := toTimeFromBoundary(max)
		if err != nil {
			return err
		}

		if skip {
			return nil
		}

		if t.After(maxTime) {
			return fmt.Errorf("must be no later than %s", maxTime.Format(time.RFC3339))
		}

		return nil
	})
}

// TimeLT validates that a time is strictly less than the given maximum.
// The max parameter can be either time.Time or *time.Time.
func TimeLT(max any) v.Rule {
	return v.By(func(value any) error {
		t, err := toTime(value)
		if err != nil {
			return err
		}

		if t.IsZero() {
			return nil
		}

		maxTime, skip, err := toTimeFromBoundary(max)
		if err != nil {
			return err
		}

		if skip {
			return nil
		}

		if !t.Before(maxTime) {
			return fmt.Errorf("must be before %s", maxTime.Format(time.RFC3339))
		}

		return nil
	})
}

// TimeBetween validates that a time is between min and max (inclusive).
// The min and max parameters can be either time.Time or *time.Time.
func TimeBetween(min, max any) v.Rule {
	return v.By(func(value any) error {
		t, err := toTime(value)
		if err != nil {
			return err
		}

		if t.IsZero() {
			return nil
		}

		minTime, skipMin, err := toTimeFromBoundary(min)
		if err != nil {
			return err
		}

		if skipMin {
			return nil
		}

		maxTime, skipMax, err := toTimeFromBoundary(max)
		if err != nil {
			return err
		}

		if skipMax {
			return nil
		}

		if t.Before(minTime) || t.After(maxTime) {
			return fmt.Errorf("must be between %s and %s", minTime.Format(time.RFC3339), maxTime.Format(time.RFC3339))
		}

		return nil
	})
}
