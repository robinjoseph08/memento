package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewConfiguresServer(t *testing.T) {
	t.Parallel()

	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:0", srv.Addr)
	assert.Equal(t, 3*time.Second, srv.ReadHeaderTimeout)
	assert.IsType(t, &echo.Echo{}, srv.Handler)
}

func TestHealthRoute(t *testing.T) {
	t.Parallel()

	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.JSONEq(t, `{"healthy":true}`, recorder.Body.String())
}

func TestNotFoundResponse(t *testing.T) {
	t.Parallel()

	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "not_found", response.Error.Code)
}

func TestEmbeddedFrontendDoesNotHandleUnknownAPIRoutes(t *testing.T) {
	t.Parallel()

	frontend := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("frontend"))
	})
	srv, err := newServer(config.NewForTest(), frontend)
	require.NoError(t, err)

	apiRecorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(apiRecorder, httptest.NewRequest(http.MethodGet, "/api/missing", nil))
	assert.Equal(t, http.StatusNotFound, apiRecorder.Code)
	assert.Contains(t, apiRecorder.Body.String(), "not_found")

	frontendRecorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(frontendRecorder, httptest.NewRequest(http.MethodGet, "/settings", nil))
	assert.Equal(t, http.StatusOK, frontendRecorder.Code)
	assert.Equal(t, "frontend", frontendRecorder.Body.String())
}

func TestMethodNotAllowedResponse(t *testing.T) {
	t.Parallel()

	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/health", nil))

	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
	assert.Contains(t, recorder.Header().Get(echo.HeaderAllow), http.MethodGet)
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "method_not_allowed", response.Error.Code)
}

func TestCORS(t *testing.T) {
	t.Parallel()

	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set(echo.HeaderOrigin, "http://localhost:5173")
	recorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, req)

	assert.Equal(t, "*", recorder.Header().Get(echo.HeaderAccessControlAllowOrigin))
}

func TestRecoveryMiddleware(t *testing.T) {
	t.Parallel()

	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	e := srv.Handler.(*echo.Echo)
	e.GET("/panic", func(_ *echo.Context) error {
		panic("boom")
	})

	recorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
}
