package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnknownRouteReturnsStableProblemWithRequestID(t *testing.T) {
	e := New(new(health.Service))
	request := httptest.NewRequest(http.MethodGet, "/api/not-found", nil)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), `"type":"about:blank"`)
	assert.Contains(t, response.Body.String(), `"title":"Not Found"`)
	assert.Regexp(t, `"request_id":"[0-9a-f-]+"`, response.Body.String())
}

func TestBodyLimitUsesStableProblem(t *testing.T) {
	e := New(new(health.Service))
	e.POST("/api/test", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(strings.Repeat("x", 11<<20)))
	request.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	assert.Contains(t, response.Body.String(), `"status":413`)
}
