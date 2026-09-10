package fx

// RequestError assigns the transport outcome without inheriting a nested persistence error's default HTTP status.
func RequestError(status int, code, message string, cause error) error {
	return &requestError{status: status, code: code, message: message, cause: cause}
}

type requestError struct {
	status        int
	code, message string
	cause         error
}

// Error returns only the public message; the original cause remains inspectable through Unwrap.
func (e *requestError) Error() string { return e.message }

// Unwrap preserves errors.Is/As for diagnostics without choosing the HTTP status from the deepest cause.
func (e *requestError) Unwrap() error { return e.cause }
