package errcodes

import (
	"fmt"
	"net/http"
)

// Error is an API error with a stable machine-readable code.
type Error struct {
	HTTPCode int
	Message  string
	Code     string
}

func (err *Error) Error() string {
	return err.Message
}

func (err *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok &&
		other.HTTPCode == err.HTTPCode &&
		other.Message == err.Message &&
		other.Code == err.Code
}

// NotFound reports that a resource does not exist.
func NotFound(resource string) error {
	return &Error{http.StatusNotFound, resource + " not found.", "not_found"}
}

// UnsupportedMediaType reports an unsupported request content type.
func UnsupportedMediaType() error {
	return &Error{http.StatusUnsupportedMediaType, "Unsupported Media Type", "unsupported_media_type"}
}

// UnknownParameter reports a request parameter that is not part of the input
// type.
func UnknownParameter(param string) error {
	return &Error{http.StatusUnprocessableEntity, fmt.Sprintf("Unknown Parameter %q", param), "unknown_parameter"}
}

// ValidationTypeError reports a value that could not be converted to the
// expected type.
func ValidationTypeError(message string) error {
	return &Error{http.StatusUnprocessableEntity, message, "validation_type_error"}
}

// ValidationError reports a request that failed validation.
func ValidationError(message string) error {
	return &Error{http.StatusUnprocessableEntity, message, "validation_error"}
}

// MalformedPayload reports a request body that could not be decoded.
func MalformedPayload() error {
	return &Error{http.StatusBadRequest, "Malformed Payload", "malformed_payload"}
}

// EmptyRequestBody reports a missing body on a request that requires one.
func EmptyRequestBody() error {
	return &Error{http.StatusBadRequest, "Request body can't be empty.", "empty_request_body"}
}
