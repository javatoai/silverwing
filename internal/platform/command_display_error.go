package platform

// commandDisplayError keeps a safe presentation separate from the original
// command error. Formatting a cause with %w would expose its unredacted text.
type commandDisplayError struct {
	message string
	cause   error
}

func (e *commandDisplayError) Error() string { return e.message }
func (e *commandDisplayError) Unwrap() error { return e.cause }

func wrapCommandDisplayError(message string, cause error) error {
	return &commandDisplayError{message: message, cause: cause}
}
