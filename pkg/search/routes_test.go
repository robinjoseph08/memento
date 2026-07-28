package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
)

type routeAuthorizer struct{ err error }

func (authorizer routeAuthorizer) AuthorizeSession(context.Context, string, string, bool) (setup.SessionActor, error) {
	return setup.SessionActor{}, authorizer.err
}

func searchHTTP(authorizer routeAuthorizer) *echo.Echo {
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(nil, authorizer))
	return e
}

func TestSearchRouteRejectsInvalidDateVariantsAsValidationErrors(t *testing.T) {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/search", strings.NewReader(`{"date":{"kind":"year","year":2026,"month":"2026-07"}}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
	response := httptest.NewRecorder()
	searchHTTP(routeAuthorizer{}).ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Contains(t, response.Body.String(), invalidRequestMessage)
}

func TestSearchRouteUsesAPOSTBodyAndRequiresARecipientSession(t *testing.T) {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/search", strings.NewReader(`{"query":"private words"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	response := httptest.NewRecorder()
	searchHTTP(routeAuthorizer{}).ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.Equal(t, "private, no-store", response.Header().Get(echo.HeaderCacheControl))
	assert.NotContains(t, request.URL.String(), "private")

	request = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/search", strings.NewReader(`{"query":"private words"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
	response = httptest.NewRecorder()
	searchHTTP(routeAuthorizer{err: setup.ErrUnauthenticated}).ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	assert.NotContains(t, response.Body.String(), "private words")
}
