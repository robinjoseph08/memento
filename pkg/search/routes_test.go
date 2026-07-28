package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
)

type routeAuthorizer struct {
	actor setup.SessionActor
	err   error
}

func (authorizer routeAuthorizer) AuthorizeSession(context.Context, string, string, bool) (setup.SessionActor, error) {
	return authorizer.actor, authorizer.err
}

func searchHTTP(authorizer routeAuthorizer) *echo.Echo {
	return searchHTTPWithHandler(NewHandler(nil, authorizer))
}

func searchHTTPWithHandler(handler *Handler) *echo.Echo {
	e := echo.New()
	requestBinder, err := binder.New()
	if err != nil {
		panic(err)
	}
	e.Binder = requestBinder
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, handler)
	return e
}

func TestSearchRouteRejectsInvalidDateVariantsWithTheProductionBinder(t *testing.T) {
	for _, body := range []string{
		`{"date":{"kind":"year"}}`,
		`{"date":{"kind":"year","year":2026,"month":"2026-07"}}`,
		`{"date":{"kind":"range","start_date":"2026-07-29","end_date":"2026-07-20"}}`,
	} {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/search", strings.NewReader(body))
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
		response := httptest.NewRecorder()
		searchHTTP(routeAuthorizer{}).ServeHTTP(response, request)
		assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
		assert.Contains(t, response.Body.String(), invalidRequestMessage)
		assert.NotContains(t, response.Body.String(), "malformed_payload")
	}
}

func TestSearchRouteRateLimitsAnAuthenticatedAccessGeneration(t *testing.T) {
	authorizer := routeAuthorizer{actor: setup.SessionActor{AccessID: uuid.MustParse("10000000-0000-0000-0000-000000000001")}}
	handler := NewHandler(nil, authorizer)
	handler.limiter.limit = 0
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/search", strings.NewReader(`{"query":"family"}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque"})
	response := httptest.NewRecorder()

	searchHTTPWithHandler(handler).ServeHTTP(response, request)

	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Contains(t, response.Body.String(), "rate_limited")
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
