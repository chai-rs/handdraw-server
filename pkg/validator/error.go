package valx

import (
	errx "github.com/chai-rs/handdraw-server/pkg/error"
	v "github.com/go-ozzo/ozzo-validation/v4"
)

func wrapErr(err error) error {
	errs, ok := err.(v.Errors)
	if !ok {
		return errx.Wrap(err)
	}

	ctx := make(map[string]any)
	for k, v := range errs {
		ctx[k] = v.Error()
	}

	return errx.With(ErrorKeyInvalidFields, ctx).Wrap(err)
}
