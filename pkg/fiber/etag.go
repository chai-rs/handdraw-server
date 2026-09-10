package fx

import (
	"errors"
	"strconv"
)

var (
	// ErrPreconditionRequired indicates a missing If-Match metadata revision.
	ErrPreconditionRequired = errors.New("precondition required")
	// ErrInvalidPrecondition rejects weak, wildcard, multiple or noncanonical revisions.
	ErrInvalidPrecondition = errors.New("invalid precondition")
)

// ParseIfMatch accepts exactly one strong quoted positive int64 revision.
func ParseIfMatch(value string) (int64, error) {
	if value == "" {
		return 0, ErrPreconditionRequired
	}

	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return 0, ErrInvalidPrecondition
	}

	raw := value[1 : len(value)-1]

	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || revision < 1 || strconv.FormatInt(revision, 10) != raw {
		return 0, ErrInvalidPrecondition
	}

	return revision, nil
}

// StrongETag formats a valid metadata revision for HTTP response headers.
func StrongETag(revision int64) (string, error) {
	if revision < 1 {
		return "", ErrInvalidPrecondition
	}

	return `"` + strconv.FormatInt(revision, 10) + `"`, nil
}
