package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/robinjoseph08/memento/pkg/binder"
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

func newServer(t *testing.T) *echo.Echo {
	t.Helper()
	e, err := New(new(health.Service))
	require.NoError(t, err)
	return e
}

func TestServerUsesCustomBinder(t *testing.T) {
	e := newServer(t)
	assert.IsType(t, new(binder.Binder), e.Binder)
}

func TestUnknownRouteReturnsStableErrorWithRequestID(t *testing.T) {
	e := newServer(t)
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
	e := newServer(t)
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

func TestChunkedBodyLimitErrorsSurviveBinding(t *testing.T) {
	e := newServer(t)
	e.Use(middleware.BodyLimit("1K"))
	e.POST("/api/bind", func(c echo.Context) error {
		payload := struct {
			Name string `json:"name" form:"name"`
		}{}
		return c.Bind(&payload)
	})

	var multipartBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&multipartBody)
	require.NoError(t, multipartWriter.WriteField("name", strings.Repeat("x", 2<<10)))
	require.NoError(t, multipartWriter.Close())
	tests := []struct {
		name        string
		body        string
		contentType string
	}{
		{"JSON", `{"name":"` + strings.Repeat("x", 2<<10) + `"}`, echo.MIMEApplicationJSON},
		{"form", url.Values{"name": {strings.Repeat("x", 2<<10)}}.Encode(), echo.MIMEApplicationForm},
		{"multipart", multipartBody.String(), multipartWriter.FormDataContentType()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/bind", nil)
			request.Body = io.NopCloser(iotest.OneByteReader(strings.NewReader(test.body)))
			request.ContentLength = -1
			request.Header.Set(echo.HeaderContentType, test.contentType)
			response := httptest.NewRecorder()
			e.ServeHTTP(response, request)
			payload := decodeError(t, response)
			assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
			assert.Equal(t, "request_entity_too_large", payload.Error.Code)
			assert.Equal(t, http.StatusRequestEntityTooLarge, payload.Error.StatusCode)
		})
	}
}

func TestBodyLimitUsesStableError(t *testing.T) {
	e := newServer(t)
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
