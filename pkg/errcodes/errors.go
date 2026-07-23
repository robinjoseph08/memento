// Package errcodes defines stable errors returned by the HTTP API.
package errcodes

import (
	"maps"
	"net/http"
)

// Error carries a safe client message, stable machine code, and HTTP status.
type Error struct {
	HTTPCode    int
	Message     string
	Code        string
	FieldErrors map[string]string
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) Is(target error) bool {
	targetError, ok := target.(*Error)
	if !ok {
		return false
	}
	return targetError.HTTPCode == e.HTTPCode &&
		targetError.Message == e.Message &&
		targetError.Code == e.Code &&
		maps.Equal(targetError.FieldErrors, e.FieldErrors)
}

// BadRequest returns a 400 error with the given safe message.
func BadRequest(message string) error {
	return &Error{HTTPCode: http.StatusBadRequest, Message: message, Code: "bad_request", FieldErrors: nil}
}

// Unauthorized returns a 401 error with the given safe message.
func Unauthorized(message string) error {
	return &Error{HTTPCode: http.StatusUnauthorized, Message: message, Code: "unauthorized", FieldErrors: nil}
}

// Forbidden returns a 403 error describing the disallowed action.
func Forbidden(action string) error {
	return &Error{HTTPCode: http.StatusForbidden, Message: action + " is not allowed.", Code: "forbidden", FieldErrors: nil}
}

// NotFound returns a 404 error naming the missing resource.
func NotFound(resource string) error {
	return &Error{HTTPCode: http.StatusNotFound, Message: resource + " not found.", Code: "not_found", FieldErrors: nil}
}

// Conflict returns a 409 error with the given safe message.
func Conflict(message string) error {
	return &Error{HTTPCode: http.StatusConflict, Message: message, Code: "conflict", FieldErrors: nil}
}

// ValidationError returns a 422 error with the given safe message.
func ValidationError(message string) error {
	return &Error{HTTPCode: http.StatusUnprocessableEntity, Message: message, Code: "validation_error", FieldErrors: nil}
}

// FieldValidationError returns a 422 error with safe per-field messages.
func FieldValidationError(message string, fieldErrors map[string]string) error {
	return &Error{
		HTTPCode:    http.StatusUnprocessableEntity,
		Message:     message,
		Code:        "validation_error",
		FieldErrors: maps.Clone(fieldErrors),
	}
}
