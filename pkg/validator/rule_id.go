package valx

import (
	"fmt"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
	v "github.com/go-ozzo/ozzo-validation/v4"
)

// NewIDRule validates the exact model-owned prefix and a canonical, nonzero KSUID.
// The wrapped sentinel remains available to domain error mapping through errors.Is.
func NewIDRule(domain, prefix string) v.Rule {
	return v.By(func(value any) error {
		id, ok := value.(string)
		if !ok || resourceid.Validate(id, prefix) != nil {
			return fmt.Errorf("invalid %s identifier: %w", domain, resourceid.ErrInvalid)
		}

		return nil
	})
}
