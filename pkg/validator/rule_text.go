package valx

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// UTF8Text accepts valid UTF-8 text without NUL, as required by PostgreSQL text columns.
// Combine it with Required and a domain-owned length rule for names.
var UTF8Text = By(func(value any) error {
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return errors.New("must be valid UTF-8 text without NUL")
	}

	return nil
})
