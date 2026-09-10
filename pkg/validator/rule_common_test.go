package valx

import (
	"testing"

	v "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/stretchr/testify/require"
)

func TestWhenNotInitialize(t *testing.T) {
	testcases := []struct {
		name    string
		initial []bool
		value   string
		isError bool
	}{
		{
			name:    "no argument applies the rules",
			initial: nil,
			value:   "",
			isError: true,
		},
		{
			name:    "no argument passes a satisfied rule",
			initial: nil,
			value:   "set",
			isError: false,
		},
		{
			name:    "false applies the rules",
			initial: []bool{false},
			value:   "",
			isError: true,
		},
		{
			name:    "true suppresses the rules",
			initial: []bool{true},
			value:   "",
			isError: false,
		},
		{
			name:    "only the first argument is read",
			initial: []bool{true, false, true},
			value:   "",
			isError: false,
		},
		{
			name:    "only the first argument is read, applying",
			initial: []bool{false, true, true},
			value:   "",
			isError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			rule := WhenNotInitialize(tc.initial...)(Required)
			err := v.Validate(tc.value, rule)

			if tc.isError {
				r.Error(err)
			} else {
				r.NoError(err)
			}
		})
	}
}

func TestWhenNotInitialize_GatesEveryField(t *testing.T) {
	type Account struct {
		Name string
		Slug string
	}

	testcases := []struct {
		name    string
		initial bool
		isError bool
	}{
		{
			name:    "initializing skips both fields",
			initial: true,
			isError: false,
		},
		{
			name:    "not initializing reports both fields",
			initial: false,
			isError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			r := require.New(t)

			account := Account{}
			gate := WhenNotInitialize(tc.initial)

			err := Struct(
				&account,
				Field(&account.Name, gate(Required)),
				Field(&account.Slug, gate(Required)),
			)

			if tc.isError {
				r.Error(err)
				r.Contains(err.Error(), "Name")
				r.Contains(err.Error(), "Slug")
			} else {
				r.NoError(err)
			}
		})
	}
}
