package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type errorPayload struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	StatusCode int    `json:"status_code"`
	RequestID  string `json:"request_id"`
}

type errorEnvelope struct {
	Error errorPayload `json:"error"`
}

func decodeError(t *testing.T, response *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
	require.Len(t, fields, 1)
	require.Contains(t, fields, "error")
	var errorFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fields["error"], &errorFields))
	require.Len(t, errorFields, 4)
	require.ElementsMatch(t, []string{"code", "message", "status_code", "request_id"}, keys(errorFields))
	var envelope errorEnvelope
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	return envelope
}

func keys(values map[string]json.RawMessage) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}

func TestUnknownRouteReturnsStableErrorWithRequestID(t *testing.T) {
	e := New(new(health.Service))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/not-found", nil)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	payload := decodeError(t, response)

	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Equal(t, "not_found", payload.Error.Code)
	assert.Equal(t, "Page not found.", payload.Error.Message)
	assert.Equal(t, http.StatusNotFound, payload.Error.StatusCode)
	assert.NotEmpty(t, payload.Error.RequestID)
}

func TestMethodNotAllowedPreservesAllowHeader(t *testing.T) {
	e := New(new(health.Service))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/health/live", nil)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	payload := decodeError(t, response)

	assert.Equal(t, http.StatusMethodNotAllowed, response.Code)
	assert.Contains(t, response.Header().Get(echo.HeaderAllow), http.MethodGet)
	assert.Equal(t, "method_not_allowed", payload.Error.Code)
	assert.Equal(t, http.StatusText(http.StatusMethodNotAllowed), payload.Error.Message)
	assert.Equal(t, http.StatusMethodNotAllowed, payload.Error.StatusCode)
	assert.NotEmpty(t, payload.Error.RequestID)
}

func TestBodyLimitUsesStableError(t *testing.T) {
	e := New(new(health.Service))
	e.POST("/api/test", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/test", strings.NewReader(strings.Repeat("x", 11<<20)))
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	payload := decodeError(t, response)

	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	assert.Equal(t, "request_entity_too_large", payload.Error.Code)
	assert.Equal(t, http.StatusText(http.StatusRequestEntityTooLarge), payload.Error.Message)
	assert.Equal(t, http.StatusRequestEntityTooLarge, payload.Error.StatusCode)
	assert.NotEmpty(t, payload.Error.RequestID)
}
