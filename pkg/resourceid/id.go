// Package resourceid validates the case-sensitive resource identifiers used by Handdraw.
package resourceid

import (
	"errors"
	"strings"

	"github.com/segmentio/ksuid"
)

// ErrInvalid identifies a malformed, noncanonical, or incorrectly typed resource ID.
var ErrInvalid = errors.New("invalid resource ID")

// New returns a cryptographically generated ID of the given registered kind.
func New(prefix string) (string, error) {
	if !known(prefix) {
		return "", ErrInvalid
	}

	id, err := ksuid.NewRandom()
	if err != nil {
		return "", err
	}

	return prefix + "_" + id.String(), nil
}

// Validate requires the exact prefix and a canonical, nonzero 20-byte KSUID.
// Input is never trimmed or case-normalized.
func Validate(value, prefix string) error {
	if !known(prefix) || len(value) != len(prefix)+28 || !strings.HasPrefix(value, prefix+"_") {
		return ErrInvalid
	}

	suffix := value[len(prefix)+1:]

	id, err := ksuid.Parse(suffix)
	if err != nil || id == ksuid.Nil || id.String() != suffix {
		return ErrInvalid
	}

	return nil
}

func known(prefix string) bool {
	switch prefix {
	case "usr", "ws", "prj", "brd", "inv", "thr", "cmt", "ast", "upl", "bci", "evt", "job", "idem", "pag", "note", "fld", "lsh", "ord", "rfnd":
		return true
	default:
		return false
	}
}
