// Package config loads explicitly exported settings without parsing flags or mutating the environment.
package config

import "github.com/kelseyhightower/envconfig"

// New decodes environment variables into T using the supplied prefix and envconfig tags.
// File loading, if required, belongs to an explicit startup step outside this package.
func New[T any](prefix string) (*T, error) {
	var value T
	if err := envconfig.Process(prefix, &value); err != nil {
		return nil, err
	}

	return &value, nil
}

// MustNew panics when configuration cannot be decoded; executable startup should prefer New.
func MustNew[T any](prefix string) *T {
	value, err := New[T](prefix)
	if err != nil {
		panic(err)
	}

	return value
}
