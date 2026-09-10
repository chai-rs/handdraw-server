package valx

import (
	"testing"

	v "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/stretchr/testify/require"
)

func TestSize(t *testing.T) {
	type args struct {
		value any
		min   int
		max   int
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
			name: "string within range",
			args: args{value: "hello", min: 2, max: 10},
			want: want{isError: false},
		},
		{
			name: "string at minimum",
			args: args{value: "hi", min: 2, max: 10},
			want: want{isError: false},
		},
		{
			name: "string at maximum",
			args: args{value: "helloworld", min: 2, max: 10},
			want: want{isError: false},
		},
		{
			name: "string below minimum",
			args: args{value: "a", min: 2, max: 10},
			want: want{isError: true, errMsg: "must contain between 2 and 10 items"},
		},
		{
			name: "string above maximum",
			args: args{value: "this is too long", min: 2, max: 10},
			want: want{isError: true, errMsg: "must contain between 2 and 10 items"},
		},
		{
			name: "unicode string length",
			args: args{value: "你好世界", min: 2, max: 10},
			want: want{isError: false},
		},
		{
			name: "slice within range",
			args: args{value: []string{"a", "b", "c"}, min: 1, max: 5},
			want: want{isError: false},
		},
		{
			name: "slice below minimum",
			args: args{value: []string{}, min: 1, max: 5},
			want: want{isError: true, errMsg: "must contain between 1 and 5 items"},
		},
		{
			name: "slice above maximum",
			args: args{value: []int{1, 2, 3, 4, 5, 6}, min: 1, max: 5},
			want: want{isError: true, errMsg: "must contain between 1 and 5 items"},
		},
		{
			name: "map within range",
			args: args{value: map[string]int{"a": 1, "b": 2}, min: 1, max: 5},
			want: want{isError: false},
		},
		{
			name: "map below minimum",
			args: args{value: map[string]int{}, min: 1, max: 5},
			want: want{isError: true, errMsg: "must contain between 1 and 5 items"},
		},
		{
			name: "map above maximum",
			args: args{value: map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "f": 6}, min: 1, max: 5},
			want: want{isError: true, errMsg: "must contain between 1 and 5 items"},
		},
		{
			name: "only minimum constraint - valid",
			args: args{value: []string{"a", "b"}, min: 2, max: 0},
			want: want{isError: false},
		},
		{
			name: "only minimum constraint - invalid",
			args: args{value: []string{"a"}, min: 2, max: 0},
			want: want{isError: true, errMsg: "must contain at least 2 items"},
		},
		{
			name: "only maximum constraint - valid",
			args: args{value: []string{"a", "b"}, min: 0, max: 5},
			want: want{isError: false},
		},
		{
			name: "only maximum constraint - invalid",
			args: args{value: []string{"a", "b", "c", "d", "e", "f"}, min: 0, max: 5},
			want: want{isError: true, errMsg: "must contain at most 5 items"},
		},
		{
			name: "nil value skips validation",
			args: args{value: nil, min: 1, max: 10},
			want: want{isError: false},
		},
		{
			name: "nil slice skips validation",
			args: args{value: ([]string)(nil), min: 1, max: 10},
			want: want{isError: false},
		},
		{
			name: "nil map skips validation",
			args: args{value: (map[string]int)(nil), min: 1, max: 10},
			want: want{isError: false},
		},
		{
			name: "pointer to string",
			args: args{value: stringPtr("hello"), min: 2, max: 10},
			want: want{isError: false},
		},
		{
			name: "pointer to slice",
			args: args{value: &[]string{"a", "b"}, min: 1, max: 5},
			want: want{isError: false},
		},
		{
			name: "pointer to nil skips validation",
			args: args{value: (*string)(nil), min: 1, max: 10},
			want: want{isError: false},
		},
		{
			name: "unsupported type",
			args: args{value: 123, min: 1, max: 10},
			want: want{isError: true, errMsg: "must be a string, slice, array, or map"},
		},
		{
			name: "array within range",
			args: args{value: [3]int{1, 2, 3}, min: 1, max: 5},
			want: want{isError: false},
		},
		{
			name: "array below minimum",
			args: args{value: [1]int{1}, min: 2, max: 5},
			want: want{isError: true, errMsg: "must contain between 2 and 5 items"},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			err := v.Validate(tc.args.value, Size(tc.args.min, tc.args.max))

			if tc.want.isError {
				r.Error(err)
				r.Contains(err.Error(), tc.want.errMsg)
			} else {
				r.NoError(err)
			}
		})
	}
}

func TestSize_WithField(t *testing.T) {
	type Data struct {
		Name string
		Tags []string
		Meta map[string]string
	}

	testcases := []struct {
		name    string
		data    Data
		isError bool
	}{
		{
			name:    "all fields valid",
			data:    Data{Name: "John", Tags: []string{"a", "b"}, Meta: map[string]string{"key": "value"}},
			isError: false,
		},
		{
			name:    "name too short",
			data:    Data{Name: "J", Tags: []string{"a", "b"}, Meta: map[string]string{"key": "value"}},
			isError: true,
		},
		{
			name:    "too few tags",
			data:    Data{Name: "John", Tags: []string{}, Meta: map[string]string{"key": "value"}},
			isError: true,
		},
		{
			name:    "too many meta keys",
			data:    Data{Name: "John", Tags: []string{"a"}, Meta: map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5", "f": "6"}},
			isError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			err := Struct(&tc.data,
				Field(&tc.data.Name, Size(2, 50)),
				Field(&tc.data.Tags, Size(1, 5)),
				Field(&tc.data.Meta, Size(0, 5)),
			)

			if tc.isError {
				r.Error(err)
			} else {
				r.NoError(err)
			}
		})
	}
}

func stringPtr(s string) *string {
	return &s
}
