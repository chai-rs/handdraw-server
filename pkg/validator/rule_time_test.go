package valx

import (
	"testing"
	"time"

	v "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/stretchr/testify/require"
)

var (
	refTime    = time.Date(2026, 2, 17, 12, 0, 0, 0, time.UTC)
	beforeRef  = refTime.Add(-1 * time.Hour)
	afterRef   = refTime.Add(1 * time.Hour)
	refTimePtr = func(t time.Time) *time.Time { return &t }
)

func TestTimeGTE(t *testing.T) {
	type args struct {
		value any
		min   time.Time
	}

	type want struct {
		isError bool
		errMsg  string
	}

	testcases := []struct {
		name string
		args args
		want want
	}{
		{
			name: "success - after minimum",
			args: args{value: afterRef, min: refTime},
			want: want{isError: false},
		},
		{
			name: "success - at minimum (equal)",
			args: args{value: refTime, min: refTime},
			want: want{isError: false},
		},
		{
			name: "error - before minimum",
			args: args{value: beforeRef, min: refTime},
			want: want{isError: true, errMsg: "must be no earlier than"},
		},
		{
			name: "success - pointer value after minimum",
			args: args{value: refTimePtr(afterRef), min: refTime},
			want: want{isError: false},
		},
		{
			name: "success - nil pointer skips validation",
			args: args{value: (*time.Time)(nil), min: refTime},
			want: want{isError: false},
		},
		{
			name: "success - zero time skips validation",
			args: args{value: time.Time{}, min: refTime},
			want: want{isError: false},
		},
		{
			name: "error - non-time value (string)",
			args: args{value: "2026-02-17T12:00:00Z", min: refTime},
			want: want{isError: true, errMsg: "must be a time.Time"},
		},
		{
			name: "error - non-time value (int)",
			args: args{value: 1234567890, min: refTime},
			want: want{isError: true, errMsg: "must be a time.Time"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			err := v.Validate(tc.args.value, TimeGTE(tc.args.min))

			if tc.want.isError {
				r.Error(err)
				r.Contains(err.Error(), tc.want.errMsg)
			} else {
				r.NoError(err)
			}
		})
	}
}

func TestTimeGT(t *testing.T) {
	type args struct {
		value any
		min   time.Time
	}

	type want struct {
		isError bool
		errMsg  string
	}

	testcases := []struct {
		name string
		args args
		want want
	}{
		{
			name: "success - after minimum",
			args: args{value: afterRef, min: refTime},
			want: want{isError: false},
		},
		{
			name: "error - at minimum (equal, not strictly after)",
			args: args{value: refTime, min: refTime},
			want: want{isError: true, errMsg: "must be after"},
		},
		{
			name: "error - before minimum",
			args: args{value: beforeRef, min: refTime},
			want: want{isError: true, errMsg: "must be after"},
		},
		{
			name: "success - pointer value after minimum",
			args: args{value: refTimePtr(afterRef), min: refTime},
			want: want{isError: false},
		},
		{
			name: "success - nil pointer skips validation",
			args: args{value: (*time.Time)(nil), min: refTime},
			want: want{isError: false},
		},
		{
			name: "success - zero time skips validation",
			args: args{value: time.Time{}, min: refTime},
			want: want{isError: false},
		},
		{
			name: "error - non-time value",
			args: args{value: "2026-02-17", min: refTime},
			want: want{isError: true, errMsg: "must be a time.Time"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			err := v.Validate(tc.args.value, TimeGT(tc.args.min))

			if tc.want.isError {
				r.Error(err)
				r.Contains(err.Error(), tc.want.errMsg)
			} else {
				r.NoError(err)
			}
		})
	}
}

func TestTimeLTE(t *testing.T) {
	type args struct {
		value any
		max   time.Time
	}

	type want struct {
		isError bool
		errMsg  string
	}

	testcases := []struct {
		name string
		args args
		want want
	}{
		{
			name: "success - before maximum",
			args: args{value: beforeRef, max: refTime},
			want: want{isError: false},
		},
		{
			name: "success - at maximum (equal)",
			args: args{value: refTime, max: refTime},
			want: want{isError: false},
		},
		{
			name: "error - after maximum",
			args: args{value: afterRef, max: refTime},
			want: want{isError: true, errMsg: "must be no later than"},
		},
		{
			name: "success - pointer value before maximum",
			args: args{value: refTimePtr(beforeRef), max: refTime},
			want: want{isError: false},
		},
		{
			name: "success - nil pointer skips validation",
			args: args{value: (*time.Time)(nil), max: refTime},
			want: want{isError: false},
		},
		{
			name: "success - zero time skips validation",
			args: args{value: time.Time{}, max: refTime},
			want: want{isError: false},
		},
		{
			name: "error - non-time value",
			args: args{value: 1234567890, max: refTime},
			want: want{isError: true, errMsg: "must be a time.Time"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			err := v.Validate(tc.args.value, TimeLTE(tc.args.max))

			if tc.want.isError {
				r.Error(err)
				r.Contains(err.Error(), tc.want.errMsg)
			} else {
				r.NoError(err)
			}
		})
	}
}

func TestTimeLT(t *testing.T) {
	type args struct {
		value any
		max   time.Time
	}

	type want struct {
		isError bool
		errMsg  string
	}

	testcases := []struct {
		name string
		args args
		want want
	}{
		{
			name: "success - before maximum",
			args: args{value: beforeRef, max: refTime},
			want: want{isError: false},
		},
		{
			name: "error - at maximum (equal, not strictly before)",
			args: args{value: refTime, max: refTime},
			want: want{isError: true, errMsg: "must be before"},
		},
		{
			name: "error - after maximum",
			args: args{value: afterRef, max: refTime},
			want: want{isError: true, errMsg: "must be before"},
		},
		{
			name: "success - pointer value before maximum",
			args: args{value: refTimePtr(beforeRef), max: refTime},
			want: want{isError: false},
		},
		{
			name: "success - nil pointer skips validation",
			args: args{value: (*time.Time)(nil), max: refTime},
			want: want{isError: false},
		},
		{
			name: "success - zero time skips validation",
			args: args{value: time.Time{}, max: refTime},
			want: want{isError: false},
		},
		{
			name: "error - non-time value",
			args: args{value: "not-a-time", max: refTime},
			want: want{isError: true, errMsg: "must be a time.Time"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			err := v.Validate(tc.args.value, TimeLT(tc.args.max))

			if tc.want.isError {
				r.Error(err)
				r.Contains(err.Error(), tc.want.errMsg)
			} else {
				r.NoError(err)
			}
		})
	}
}

func TestTimeBetween(t *testing.T) {
	type args struct {
		value any
		min   time.Time
		max   time.Time
	}

	type want struct {
		isError bool
		errMsg  string
	}

	testcases := []struct {
		name string
		args args
		want want
	}{
		{
			name: "success - within range",
			args: args{value: refTime, min: beforeRef, max: afterRef},
			want: want{isError: false},
		},
		{
			name: "success - at min boundary (inclusive)",
			args: args{value: beforeRef, min: beforeRef, max: afterRef},
			want: want{isError: false},
		},
		{
			name: "success - at max boundary (inclusive)",
			args: args{value: afterRef, min: beforeRef, max: afterRef},
			want: want{isError: false},
		},
		{
			name: "error - before range",
			args: args{value: beforeRef.Add(-1 * time.Hour), min: beforeRef, max: afterRef},
			want: want{isError: true, errMsg: "must be between"},
		},
		{
			name: "error - after range",
			args: args{value: afterRef.Add(1 * time.Hour), min: beforeRef, max: afterRef},
			want: want{isError: true, errMsg: "must be between"},
		},
		{
			name: "success - pointer value within range",
			args: args{value: refTimePtr(refTime), min: beforeRef, max: afterRef},
			want: want{isError: false},
		},
		{
			name: "success - nil pointer skips validation",
			args: args{value: (*time.Time)(nil), min: beforeRef, max: afterRef},
			want: want{isError: false},
		},
		{
			name: "success - zero time skips validation",
			args: args{value: time.Time{}, min: beforeRef, max: afterRef},
			want: want{isError: false},
		},
		{
			name: "error - non-time value",
			args: args{value: "2026-02-17", min: beforeRef, max: afterRef},
			want: want{isError: true, errMsg: "must be a time.Time"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			err := v.Validate(tc.args.value, TimeBetween(tc.args.min, tc.args.max))

			if tc.want.isError {
				r.Error(err)
				r.Contains(err.Error(), tc.want.errMsg)
			} else {
				r.NoError(err)
			}
		})
	}
}

func TestTimeRules_WithField(t *testing.T) {
	type Event struct {
		StartAt *time.Time
		EndAt   *time.Time
	}

	now := time.Now()
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)

	testcases := []struct {
		name    string
		event   Event
		isError bool
	}{
		{
			name:    "valid - both set within range",
			event:   Event{StartAt: &now, EndAt: &future},
			isError: false,
		},
		{
			name:    "valid - nil fields skip validation",
			event:   Event{StartAt: nil, EndAt: nil},
			isError: false,
		},
		{
			name:    "error - start before allowed",
			event:   Event{StartAt: &past, EndAt: &future},
			isError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			err := Struct(&tc.event,
				Field(&tc.event.StartAt, TimeGTE(now.Add(-1*time.Hour))),
				Field(&tc.event.EndAt, TimeGT(now)),
			)

			if tc.isError {
				r.Error(err)
			} else {
				r.NoError(err)
			}
		})
	}
}
