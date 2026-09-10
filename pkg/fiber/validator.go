package fx

import valx "github.com/chai-rs/handdraw-server/pkg/validator"

type structValidator struct{}

func (structValidator) Validate(value any) error {
	validatable, ok := valx.ToValidatable(value)
	if !ok {
		return nil
	}

	return validatable.Validate()
}
