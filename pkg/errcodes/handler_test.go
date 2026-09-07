package errcodes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStructuredFieldErrors(t *testing.T) {
	t.Parallel()
	e := echo.New()
	e.HTTPErrorHandler = NewHandler().Handle
	e.GET("/", func(_ *echo.Context) error {
		return ValidationFields("Check the highlighted fields.", map[string]string{"email": "Enter a valid email address.", "display_name": "Enter a display name."})
	})
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Equal(t, 422, recorder.Code)
	var response ErrorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "Enter a valid email address.", response.Error.Fields["email"])
	assert.Equal(t, "Enter a display name.", response.Error.Fields["display_name"])
}

func TestHandlerResponses(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err        error
		statusCode int
		code       string
		message    string
	}{
		"application error": {NotFound("Page"), http.StatusNotFound, "not_found", "Page not found."},
		"echo error":        {echo.NewHTTPError(http.StatusTeapot, "Short and stout"), http.StatusTeapot, "short_and_stout", "Short and stout"},
		"generic error":     {errors.New("database exploded"), http.StatusInternalServerError, "internal_server_error", "Internal Server Error"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			e.HTTPErrorHandler = NewHandler().Handle
			e.GET("/", func(_ *echo.Context) error { return test.err })
			recorder := httptest.NewRecorder()
			e.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

			assert.Equal(t, test.statusCode, recorder.Code)
			var response struct {
				Error struct {
					Code       string `json:"code"`
					Message    string `json:"message"`
					StatusCode int    `json:"status_code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, test.code, response.Error.Code)
			assert.Equal(t, test.message, response.Error.Message)
			assert.Equal(t, test.statusCode, response.Error.StatusCode)
		})
	}
}

func TestHandlerDoesNotWriteCommittedResponse(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.HTTPErrorHandler = NewHandler().Handle
	e.GET("/", func(c *echo.Context) error {
		require.NoError(t, c.String(http.StatusOK, "already written"))
		return errors.New("late error")
	})
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "already written", recorder.Body.String())
}

func TestHandlerIgnoresCanceledRequests(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.HTTPErrorHandler = NewHandler().Handle
	e.GET("/", func(_ *echo.Context) error { return context.Canceled })
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	e.ServeHTTP(recorder, request.WithContext(ctx))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Empty(t, recorder.Body.String())
}

func TestHandlerDoesNotHideFailureJoinedWithCancellation(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.HTTPErrorHandler = NewHandler().Handle
	e.GET("/", func(_ *echo.Context) error {
		return errors.Join(context.Canceled, errors.New("database failed"))
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	e.ServeHTTP(recorder, request.WithContext(ctx))

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
}
