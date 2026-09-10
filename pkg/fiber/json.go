package fx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

func decodeJSON(data []byte, value any) error {
	if !utf8.Valid(data) {
		return errors.New("invalid JSON encoding")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(value); err != nil {
		return err
	}

	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("expected one JSON value")
	}

	return nil
}
