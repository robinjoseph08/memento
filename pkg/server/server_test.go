package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/golib/logger"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMutationOriginAndJSON(t *testing.T) {
	t.Parallel()
	cfg := config.NewForTest()
	cfg.PublicURL = "https://photos.example.test"
	cfg.AppEnv = "development"
	cfg.Hostname = "photos-local"
	srv, err := newServer(cfg, nil)
	require.NoError(t, err)
	e := srv.Handler.(*echo.Echo)
	e.POST("/api/mutate", func(c *echo.Context) error { return c.NoContent(204) })
	for _, tc := range []struct {
		origin, host, content string
		status                int
		// bearer and path are the Mobile App's cases: a session token in the
		// Authorization header, or the code exchange that precedes one.
		bearer, path string
	}{
		{"https://photos.example.test", "photos.example.test", "application/json", 204, "", ""},
		{"https://photos.example.test", "internal:3579", "application/json", 204, "", ""},
		{"https://evil.test", "photos.example.test", "application/json", 403, "", ""},
		{"", "photos.example.test", "application/json", 403, "", ""},
		{"null", "photos.example.test", "application/json", 403, "", ""},
		{"http://localhost:5173", "localhost:5173", "application/json", 204, "", ""},
		{"http://127.0.0.1:5173", "127.0.0.1:5173", "application/json", 204, "", ""},
		{"http://photos-local:5173", "photos-local:5173", "application/json", 204, "", ""},
		{"http://photos-local.local:5173", "photos-local.local:5173", "application/json", 204, "", ""},
		{"http://photos-local:5173", "photos-local:3579", "application/json", 403, "", ""},
		// The Mobile App's browser sheet reaches Vite by this machine's address on
		// the network, as mise start:mobile hands it out.
		{"http://192.168.2.166:5173", "192.168.2.166:5173", "application/json", 204, "", ""},
		{"http://10.0.0.5:5173", "10.0.0.5:5173", "application/json", 204, "", ""},
		{"http://172.16.0.5:5173", "172.16.0.5:5173", "application/json", 204, "", ""},
		{"http://192.168.2.166:5173", "192.168.2.166:3579", "application/json", 403, "", ""},
		{"http://203.0.113.5:5173", "203.0.113.5:5173", "application/json", 403, "", ""},
		{"https://192.168.2.166:5173", "192.168.2.166:5173", "application/json", 403, "", ""},
		{"https://photos-local:5173", "photos-local:5173", "application/json", 403, "", ""},
		{"https://evil.test", "evil.test:443", "application/json", 403, "", ""},
		{"https://photos.example.test", "photos.example.test", "application/x-www-form-urlencoded", 415, "", ""},
		{"https://photos.example.test", "photos.example.test", "", 415, "", ""},
		{"", "photos.example.test", "application/json", 204, "Bearer " + strings.Repeat("t", 43), ""},
		{"https://evil.test", "photos.example.test", "application/json", 204, "Bearer " + strings.Repeat("t", 43), ""},
		{"", "photos.example.test", "application/x-www-form-urlencoded", 415, "Bearer " + strings.Repeat("t", 43), ""},
		{"", "photos.example.test", "application/json", 403, "Basic dXNlcjpwYXNz", ""},
		{"", "photos.example.test", "application/json", 403, "Bearer", ""},
		// No identity routes are registered here, so passing the Origin check
		// lands on the API's not-found answer instead of 403.
		{"", "photos.example.test", "application/json", 404, "", identity.MobileExchangePath},
		{"https://evil.test", "photos.example.test", "application/json", 404, "", identity.MobileExchangePath},
		{"", "photos.example.test", "", 415, "", identity.MobileExchangePath},
	} {
		path := tc.path
		if path == "" {
			path = "/api/mutate"
		}
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", tc.content)
		if tc.bearer != "" {
			req.Header.Set("Authorization", tc.bearer)
		}
		req.Header.Set("X-Forwarded-Host", "evil.test")
		recorder := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recorder, req)
		assert.Equal(t, tc.status, recorder.Code, "%+v", tc)
		assert.Empty(t, recorder.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	}
}

func TestProductionMutationOriginRequiresPublicURL(t *testing.T) {
	t.Parallel()
	cfg := config.NewForTest()
	cfg.PublicURL = "https://photos.example.test"
	cfg.AppEnv = "production"
	cfg.Hostname = "photos-local"
	srv, err := newServer(cfg, nil)
	require.NoError(t, err)
	e := srv.Handler.(*echo.Echo)
	e.POST("/api/mutate", func(c *echo.Context) error { return c.NoContent(204) })
	req := httptest.NewRequest(http.MethodPost, "/api/mutate", strings.NewReader(`{}`))
	req.Host = "photos-local:5173"
	req.Header.Set("Origin", "http://photos-local:5173")
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	req.Host = "192.168.2.166:5173"
	req.Header.Set("Origin", "http://192.168.2.166:5173")
	recorder = httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusForbidden, recorder.Code, "a private address is a development allowance only")
	// The Mobile App has no Origin to offer in production either.
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 43))
	recorder = httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

// The QA fixture runs in the test environment and is opened from other
// machines on the network by this machine's name or address.
func TestTestEnvironmentAcceptsNetworkOrigins(t *testing.T) {
	t.Parallel()
	cfg := config.NewForTest()
	cfg.PublicURL = "http://127.0.0.1:53000"
	cfg.AppEnv = "test"
	cfg.Hostname = "robin-m3"
	srv, err := newServer(cfg, nil)
	require.NoError(t, err)
	e := srv.Handler.(*echo.Echo)
	e.POST("/api/mutate", func(c *echo.Context) error { return c.NoContent(204) })
	for _, tc := range []struct {
		origin, host string
		status       int
	}{
		{"http://robin-m3.local:53000", "robin-m3.local:53000", 204},
		{"http://192.168.2.166:53000", "192.168.2.166:53000", 204},
		{"http://evil.test:53000", "evil.test:53000", 403},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/mutate", strings.NewReader(`{}`))
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recorder, req)
		assert.Equal(t, tc.status, recorder.Code, "%+v", tc)
	}
}

func TestDatabaseControlsHealth(t *testing.T) {
	t.Parallel()
	srv, err := newServer(config.NewForTest(), nil, dependencies{health: func(context.Context) error { return errors.New("private database error") }})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	assert.Equal(t, 503, recorder.Code)
	assert.JSONEq(t, `{"healthy":false}`, recorder.Body.String())
}

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

	assert.Empty(t, recorder.Header().Get(echo.HeaderAccessControlAllowOrigin))
}

func unexpectedErrorAtKnownOrigin() error {
	return fmt.Errorf("load records: %w", errorstack.Capture(errors.New("database failed")))
}

func panicErrorAtKnownOrigin() {
	panic(errors.New("panic failed"))
}

func TestRecoveryMiddlewareRepanicsAbortHandler(t *testing.T) {
	t.Parallel()

	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	e := srv.Handler.(*echo.Echo)
	e.GET("/abort", func(_ *echo.Context) error { panic(http.ErrAbortHandler) })

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		srv.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
	})
}

func TestRecoveryMiddlewareHandlesWrappedAbortHandler(t *testing.T) {
	t.Parallel()

	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	e := srv.Handler.(*echo.Echo)
	e.GET("/abort", func(_ *echo.Context) error {
		panic(fmt.Errorf("unexpected abort: %w", http.ErrAbortHandler))
	})
	recorder := httptest.NewRecorder()

	srv.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/abort", nil))
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
}

//nolint:paralleltest // Golib's logger output is process-global.
func TestUnexpectedErrorLogsCreationSiteStack(t *testing.T) {
	var output bytes.Buffer
	previousOutput := logger.Output()
	logger.SetOutput(&output)
	t.Cleanup(func() { logger.SetOutput(previousOutput) })

	srv, err := newServer(config.NewForTest(), nil)
	require.NoError(t, err)
	e := srv.Handler.(*echo.Echo)
	e.GET("/error", func(_ *echo.Context) error { return unexpectedErrorAtKnownOrigin() })
	e.GET("/panic", func(_ *echo.Context) error {
		panicErrorAtKnownOrigin()
		return nil
	})
	e.GET("/expected", func(_ *echo.Context) error { return errcodes.NotFound("Page") })

	for _, test := range []struct {
		path   string
		origin string
	}{
		{path: "/error", origin: "server.unexpectedErrorAtKnownOrigin"},
		{path: "/panic", origin: "server.panicErrorAtKnownOrigin"},
	} {
		output.Reset()
		recorder := httptest.NewRecorder()
		srv.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))

		assert.Equal(t, http.StatusInternalServerError, recorder.Code)
		assert.Contains(t, output.String(), `"message":"server error"`)
		assert.Contains(t, output.String(), test.origin)
		assert.Contains(t, output.String(), "server_test.go")
	}

	output.Reset()
	recorder := httptest.NewRecorder()
	srv.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/expected", nil))
	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.NotContains(t, output.String(), `"message":"server error"`)
	assert.NotContains(t, output.String(), `"stack":`)
}
