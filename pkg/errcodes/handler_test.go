package errcodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	goliblogger "github.com/robinjoseph08/golib/echo/v4/middleware/logger"
	baselogger "github.com/robinjoseph08/golib/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serveError(err error) *httptest.ResponseRecorder {
	e := echo.New()
	e.Use(goliblogger.Middleware())
	e.HTTPErrorHandler = NewHandler().Handle
	e.GET("/error", func(echo.Context) error { return err })
	response := httptest.NewRecorder()
	e.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/error", nil))
	return response
}

func decodeError(t *testing.T, response *httptest.ResponseRecorder) Problem {
	t.Helper()
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Len(t, envelope, 1)
	require.Contains(t, envelope, "error")
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope["error"], &fields))
	require.Contains(t, fields, "code")
	require.Contains(t, fields, "message")
	require.Contains(t, fields, "status_code")
	require.Contains(t, fields, "request_id")
	if _, hasFieldErrors := fields["field_errors"]; hasFieldErrors {
		require.Len(t, fields, 5)
	} else {
		require.Len(t, fields, 4)
	}
	var payload Problem
	require.NoError(t, json.Unmarshal(envelope["error"], &payload))
	return payload
}

func TestProblemResponsePreservesWireShape(t *testing.T) {
	response := ProblemResponse{Error: Problem{
		Code:        "validation_error",
		Message:     "Check the highlighted fields.",
		StatusCode:  http.StatusUnprocessableEntity,
		FieldErrors: map[string]string{"email": "Enter a valid email address."},
		RequestID:   "request-id",
	}}

	contents, err := json.Marshal(response)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"error": {
			"code": "validation_error",
			"message": "Check the highlighted fields.",
			"status_code": 422,
			"field_errors": {"email": "Enter a valid email address."},
			"request_id": "request-id"
		}
	}`, string(contents))
}

func TestProblemResponseOmitsEmptyOptionalFields(t *testing.T) {
	response := ProblemResponse{Error: Problem{
		Code:       "not_found",
		Message:    "Photo not found.",
		StatusCode: http.StatusNotFound,
	}}

	contents, err := json.Marshal(response)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"error": {
			"code": "not_found",
			"message": "Photo not found.",
			"status_code": 404
		}
	}`, string(contents))
}

func TestHandlerUsesWrappedErrorCode(t *testing.T) {
	response := serveError(fmt.Errorf("load photo: %w", NotFound("Photo")))
	payload := decodeError(t, response)

	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, "not_found", payload.Code)
	assert.Equal(t, "Photo not found.", payload.Message)
	assert.Equal(t, http.StatusNotFound, payload.StatusCode)
	assert.Empty(t, payload.FieldErrors)
	assert.NotEmpty(t, payload.RequestID)
}

func TestHandlerSanitizesUnknownErrorsInResponsesAndLogs(t *testing.T) {
	var logs bytes.Buffer
	previousOutput := baselogger.Output()
	baselogger.SetOutput(&logs)
	t.Cleanup(func() { baselogger.SetOutput(previousOutput) })

	response := serveError(errors.New("postgresql://user:secret@private/recipient-data"))
	payload := decodeError(t, response)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Equal(t, "internal_server_error", payload.Code)
	assert.Equal(t, http.StatusText(http.StatusInternalServerError), payload.Message)
	assert.Equal(t, http.StatusInternalServerError, payload.StatusCode)
	assert.Empty(t, payload.FieldErrors)
	assert.NotEmpty(t, payload.RequestID)
	assert.NotContains(t, response.Body.String(), "secret")
	assert.NotContains(t, logs.String(), "secret")
	assert.NotContains(t, logs.String(), "private")
	assert.NotContains(t, logs.String(), "recipient")
}

func TestHandlerDerivesEchoErrorsFromHTTPStatus(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		statusCode int
		code       string
		message    string
	}{
		{"non-string message", echo.NewHTTPError(http.StatusBadRequest, map[string]string{"unsafe": "detail"}), http.StatusBadRequest, "bad_request", "Bad Request"},
		{"unsafe string message", echo.NewHTTPError(http.StatusBadRequest, "database secret"), http.StatusBadRequest, "bad_request", "Bad Request"},
		{"server error", echo.NewHTTPError(http.StatusInternalServerError, "database secret"), http.StatusInternalServerError, "internal_server_error", "Internal Server Error"},
		{"invalid low status", echo.NewHTTPError(99, "database secret"), http.StatusInternalServerError, "internal_server_error", "Internal Server Error"},
		{"nonstandard status", echo.NewHTTPError(499, "database secret"), http.StatusInternalServerError, "internal_server_error", "Internal Server Error"},
		{"invalid high status", echo.NewHTTPError(600, "database secret"), http.StatusInternalServerError, "internal_server_error", "Internal Server Error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := serveError(test.err)
			payload := decodeError(t, response)
			assert.Equal(t, test.statusCode, response.Code)
			assert.Equal(t, test.code, payload.Code)
			assert.Equal(t, test.message, payload.Message)
			assert.Equal(t, test.statusCode, payload.StatusCode)
			assert.Empty(t, payload.FieldErrors)
			assert.NotEmpty(t, payload.RequestID)
			assert.NotContains(t, response.Body.String(), "secret")
			assert.NotContains(t, response.Body.String(), "unsafe")
		})
	}
}

func TestHandlerIncludesValidationFieldErrors(t *testing.T) {
	response := serveError(FieldValidationError("Check the highlighted fields.", map[string]string{
		"email": "Enter a valid email address.",
	}))
	payload := decodeError(t, response)

	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Equal(t, "validation_error", payload.Code)
	assert.Equal(t, "Check the highlighted fields.", payload.Message)
	assert.Equal(t, http.StatusUnprocessableEntity, payload.StatusCode)
	assert.Equal(t, map[string]string{"email": "Enter a valid email address."}, payload.FieldErrors)
	assert.NotEmpty(t, payload.RequestID)
}

func TestErrorSupportsErrorsIs(t *testing.T) {
	require.ErrorIs(t, fmt.Errorf("wrapped: %w", NotFound("Photo")), NotFound("Photo"))
	require.NotErrorIs(t, NotFound("Photo"), NotFound("Person"))
}
