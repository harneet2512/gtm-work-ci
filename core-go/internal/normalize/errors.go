package normalize

import (
	"errors"
	"fmt"
)

// Error codes carried by ValidationError; the API returns them in the error envelope (HTTP 422).
const (
	// CodeInvalidEvent: the event violates its contract or is internally inconsistent.
	CodeInvalidEvent = "invalid_event"
	// CodeUnsupportedEvent: well-formed, but no normalization.md row covers this combination.
	CodeUnsupportedEvent = "unsupported_event"
)

// ValidationError is the typed error for every input problem. Callers map it to HTTP 422.
type ValidationError struct {
	Code    string
	Message string
}

func (e *ValidationError) Error() string { return e.Code + ": " + e.Message }

func invalid(format string, args ...any) error {
	return &ValidationError{Code: CodeInvalidEvent, Message: fmt.Sprintf(format, args...)}
}

func unsupported(format string, args ...any) error {
	return &ValidationError{Code: CodeUnsupportedEvent, Message: fmt.Sprintf(format, args...)}
}

// IsValidation reports whether err is (or wraps) a *ValidationError.
func IsValidation(err error) bool {
	var verr *ValidationError
	return errors.As(err, &verr)
}
