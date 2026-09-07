package errcodes

// FieldError attaches field-specific messages while preserving the shared error code.
type FieldError struct {
	Cause  error
	Fields map[string]string
}

func (e *FieldError) Error() string { return e.Cause.Error() }
func (e *FieldError) Unwrap() error { return e.Cause }

func ValidationFields(message string, fields map[string]string) error {
	return &FieldError{Cause: ValidationError(message), Fields: fields}
}
