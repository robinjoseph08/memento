package repairs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
)

type routeAuthorizer struct {
	err error
}

func (authorizer *routeAuthorizer) AuthorizeCurator(context.Context, string, string, bool) (setup.CuratorSession, error) {
	return setup.CuratorSession{}, authorizer.err
}

func repairHTTP(authorizer Authorizer) *echo.Echo {
	e := echo.New()
	RegisterRoutes(e, NewHandler(nil, authorizer))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}

func repairRequest(e *echo.Echo, method, path, session, csrf string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, nil)
	if session != "" {
		request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: session})
	}
	if csrf != "" {
		request.Header.Set(setup.CSRFHeader, csrf)
	}
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func TestRepairRoutesAreCuratorOnlyBeforePrivateEvidenceIsRead(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/repairs"},
		{http.MethodPost, "/api/repairs/reconcile"},
		{http.MethodPost, "/api/repairs/people/link"},
		{http.MethodPost, "/api/repairs/people/not-an-id/confirm"},
		{http.MethodPost, "/api/repairs/people/not-an-id/reject"},
		{http.MethodPost, "/api/repairs/media/not-an-id/confirm"},
		{http.MethodPost, "/api/repairs/media/not-an-id/reject"},
	} {
		response := repairRequest(repairHTTP(&routeAuthorizer{}), route.method, route.path, "", "")
		assert.Equal(t, http.StatusUnauthorized, response.Code, "%s %s", route.method, route.path)
		assert.NotContains(t, response.Body.String(), "checksum")
		assert.NotContains(t, response.Body.String(), "path")
	}
}

func TestRecipientSessionsCannotDiscoverRepairRoutes(t *testing.T) {
	authorizer := &routeAuthorizer{err: setup.ErrNotCurator}
	response := repairRequest(repairHTTP(authorizer), http.MethodGet, "/api/repairs", "recipient-session", "")
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.NotContains(t, response.Body.String(), "repair")
	assert.NotContains(t, response.Body.String(), "Immich")
}

func TestRepairMutationsRequireSessionBoundCSRF(t *testing.T) {
	for _, path := range []string{
		"/api/repairs/reconcile",
		"/api/repairs/people/link",
		"/api/repairs/people/11111111-1111-4111-8111-111111111111/confirm",
		"/api/repairs/media/11111111-1111-4111-8111-111111111111/reject",
	} {
		authorizer := &routeAuthorizer{err: setup.ErrCSRF}
		response := repairRequest(repairHTTP(authorizer), http.MethodPost, path, "session", "wrong")
		assert.Equal(t, http.StatusForbidden, response.Code, path)
		assert.NotContains(t, response.Body.String(), "wrong")
	}
}
