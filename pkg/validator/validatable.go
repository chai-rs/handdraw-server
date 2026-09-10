package valx

// Validatable is implemented by types that can validate themselves.
type Validatable interface {
	Validate() error
}

// ToValidatable reports whether v implements Validatable, returning it as one
// when it does.
func ToValidatable(v any) (Validatable, bool) {
	validatable, ok := v.(Validatable)
	return validatable, ok
}
