package activity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type curatorWorkStub struct {
	response CuratorWorkResponse
	called   bool
}

func (stub *curatorWorkStub) ListCuratorWork(context.Context) (CuratorWorkResponse, error) {
	stub.called = true
	return stub.response, nil
}

type curatorAuthorizerStub struct{ err error }

func (stub curatorAuthorizerStub) AuthorizeCurator(context.Context, string, string, bool) (setup.CuratorSession, error) {
	return setup.CuratorSession{}, stub.err
}

func TestCuratorWorkRouteDeclaresCuratorPolicy(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(&curatorWorkStub{}, curatorAuthorizerStub{}))

	routes := e.Routes()
	require.Len(t, routes, 1)
	assert.Equal(t, "GET", routes[0].Method)
	assert.Equal(t, "/api/activity/curator/work", routes[0].Path)
	assert.Equal(t, curatorWorkPolicy, routes[0].Name)
}

func TestCuratorWorkRouteRequiresCuratorAuthorization(t *testing.T) {
	for _, test := range []struct {
		name       string
		cookie     bool
		authErr    error
		wantStatus int
	}{
		{name: "missing session", wantStatus: http.StatusUnauthorized},
		{name: "invalid session", cookie: true, authErr: setup.ErrUnauthenticated, wantStatus: http.StatusUnauthorized},
		{name: "non curator", cookie: true, authErr: setup.ErrNotCurator, wantStatus: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &curatorWorkStub{}
			e := echo.New()
			e.HTTPErrorHandler = errcodes.NewHandler().Handle
			RegisterRoutes(e, NewHandler(service, curatorAuthorizerStub{err: test.authErr}))
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/activity/curator/work", nil)
			if test.cookie {
				request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque-session"})
			}
			response := httptest.NewRecorder()

			e.ServeHTTP(response, request)

			assert.Equal(t, test.wantStatus, response.Code)
			assert.False(t, service.called, "unauthorized callers must not read durable work items")
		})
	}
}

func TestCuratorWorkRouteReturnsOnlyAfterAuthorization(t *testing.T) {
	service := &curatorWorkStub{response: CuratorWorkResponse{Items: []CuratorWorkItem{{
		ID: "7", Kind: "delivery_problem", SourceKind: "notification_batch", SourceID: "safe-source", Diagnostic: "recipient_rejected",
	}}}}
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(service, curatorAuthorizerStub{}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/activity/curator/work", nil)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque-session"})
	response := httptest.NewRecorder()

	e.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	assert.True(t, service.called)
	assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
	assert.Contains(t, response.Body.String(), `"diagnostic":"recipient_rejected"`)
	assert.NotContains(t, response.Body.String(), "opaque-session")
}
