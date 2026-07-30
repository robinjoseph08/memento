package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func securityTestServer(t *testing.T) (*echo.Echo, *int) {
	t.Helper()
	middleware, err := browserSecurity("https://photos.example")
	require.NoError(t, err)
	calls := 0
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	e.Use(middleware)
	handler := func(c echo.Context) error {
		calls++
		return c.NoContent(http.StatusNoContent)
	}
	e.POST("/api/mutate", handler)
	e.POST("/api/email/preferences/unsubscribe", handler)
	return e, &calls
}

func TestBrowserSecurityDeniesUnapprovedOriginsBeforeHandlers(t *testing.T) {
	for _, origin := range []string{
		"null",
		"https://evil.example",
		"http://photos.example",
		"https://photos.example:444",
		"https://photos.example/path",
		"not an origin",
	} {
		t.Run(origin, func(t *testing.T) {
			e, calls := securityTestServer(t)
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/mutate", strings.NewReader(`{"ok":true}`))
			request.Header.Set(echo.HeaderOrigin, origin)
			request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			response := httptest.NewRecorder()
			e.ServeHTTP(response, request)

			assert.Equal(t, http.StatusForbidden, response.Code)
			assert.Zero(t, *calls)
			assert.Empty(t, response.Header().Get(echo.HeaderAccessControlAllowOrigin))
			assert.NotContains(t, response.Body.String(), origin)
		})
	}
}

func TestBrowserSecurityCanonicalizesConfiguredAndBrowserOrigins(t *testing.T) {
	middleware, err := browserSecurity("https://PHOTOS.example:443")
	require.NoError(t, err)
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	e.Use(middleware)
	e.POST("/api/mutate", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/mutate", strings.NewReader(`{"ok":true}`))
	request.Header.Set(echo.HeaderOrigin, "https://photos.example")
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, "https://photos.example", response.Header().Get(echo.HeaderAccessControlAllowOrigin))
}

func TestBrowserSecurityAllowsOnlyTheConfiguredOrigin(t *testing.T) {
	e, calls := securityTestServer(t)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/mutate", strings.NewReader(`{"ok":true}`))
	request.Header.Set(echo.HeaderOrigin, "https://photos.example")
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON+"; charset=utf-8")
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, 1, *calls)
	assert.Equal(t, "https://photos.example", response.Header().Get(echo.HeaderAccessControlAllowOrigin))
	assert.Equal(t, "Origin", response.Header().Get(echo.HeaderVary))
	assert.Equal(t, "true", response.Header().Get(echo.HeaderAccessControlAllowCredentials))
}

func TestBrowserSecurityAnswersApprovedPreflightsWithoutCallingHandlers(t *testing.T) {
	e, calls := securityTestServer(t)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/api/mutate", nil)
	request.Header.Set(echo.HeaderOrigin, "https://photos.example")
	request.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodPost)
	request.Header.Set(echo.HeaderAccessControlRequestHeaders, "content-type, x-memento-csrf")
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Zero(t, *calls)
	assert.Equal(t, "https://photos.example", response.Header().Get(echo.HeaderAccessControlAllowOrigin))
	assert.Contains(t, response.Header().Get(echo.HeaderAccessControlAllowMethods), http.MethodPost)
	assert.Contains(t, strings.ToLower(response.Header().Get(echo.HeaderAccessControlAllowHeaders)), "x-memento-csrf")
}

func TestBrowserSecurityRejectsSimpleMutationContentTypes(t *testing.T) {
	for _, contentType := range []string{
		echo.MIMEApplicationForm,
		echo.MIMEApplicationForm + "; charset=utf-8",
		echo.MIMEMultipartForm + "; boundary=boundary",
		echo.MIMETextPlain,
		echo.MIMETextPlain + "; charset=utf-8",
	} {
		t.Run(contentType, func(t *testing.T) {
			e, calls := securityTestServer(t)
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/mutate", strings.NewReader("value=secret"))
			request.Header.Set(echo.HeaderContentType, contentType)
			response := httptest.NewRecorder()
			e.ServeHTTP(response, request)

			assert.Equal(t, http.StatusUnsupportedMediaType, response.Code)
			assert.Zero(t, *calls)
			assert.NotContains(t, response.Body.String(), "secret")
		})
	}
}

func TestBrowserSecurityPreservesTokenAuthorizedUnsubscribeForms(t *testing.T) {
	e, calls := securityTestServer(t)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/email/preferences/unsubscribe?token=opaque", strings.NewReader("List-Unsubscribe=One-Click"))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, 1, *calls)
}
