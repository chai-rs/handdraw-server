package model

import (
	"time"

	valx "github.com/chai-rs/handdraw-server/pkg/validator"
)

// Position is the last visible row in updated_at DESC, id DESC ordering.
type Position struct {
	UpdatedAt time.Time `json:"updated_at"`
	ID        string    `json:"id"`
}

// PageRequest selects a bounded keyset page within an explicit workspace scope.
type PageRequest struct {
	Limit int       `json:"limit"`
	After *Position `json:"after,omitempty"`
}

// Normalize validates a page and supplies the API's default limit.
func (p PageRequest) Normalize(prefix string) (PageRequest, error) {
	if p.Limit == 0 {
		p.Limit = 50
	}

	if err := p.Validate(prefix); err != nil {
		return PageRequest{}, err
	}

	if p.After != nil {
		position := *p.After
		p.After = &position
	}

	return p, nil
}

// Page returns ordered rows and a position for the next page, if another row exists.
// Application transport must bind any encoded cursor to the workspace/filter/order.
type Page[T any] struct {
	Items []T       `json:"items"`
	Next  *Position `json:"next,omitempty"`
}

// Validate checks a normalized page request without applying defaults.
func (p PageRequest) Validate(prefix string) error {
	if err := valx.Struct(&p, valx.Field(&p.Limit, valx.Required, valx.Min(1), valx.Max(100))); err != nil {
		return ErrInvalidPage
	}

	if p.After != nil {
		return p.After.Validate(prefix)
	}

	return nil
}

// Validate checks the timestamp and resource kind of a keyset position.
func (p Position) Validate(prefix string) error {
	if err := valx.Struct(&p,
		valx.Field(&p.UpdatedAt, valx.Required),
		valx.Field(&p.ID, valx.NewIDRule("cursor", prefix)),
	); err != nil {
		return ErrInvalidPage
	}

	return nil
}
