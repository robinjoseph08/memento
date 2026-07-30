package engagement

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type routeServiceStub struct {
	recorded bool
	detail   RecipientDetail
}

func (stub *routeServiceStub) RecordBrowserEvent(context.Context, setup.SessionActor, BrowserEventRequest) error {
	stub.recorded = true
	return nil
}
func (stub *routeServiceStub) Recipient(context.Context, uuid.UUID, string, int) (RecipientDetail, error) {
	return stub.detail, nil
}
func (stub *routeServiceStub) MediaOpeners(context.Context, uuid.UUID) (MediaOpenersResponse, error) {
	return MediaOpenersResponse{}, nil
}

type routeAuthorizerStub struct {
	actor      setup.SessionActor
	sessionErr error
	curatorErr error
}

func (stub routeAuthorizerStub) AuthorizeSession(context.Context, string, string, bool) (setup.SessionActor, error) {
	return stub.actor, stub.sessionErr
}
func (stub routeAuthorizerStub) AuthorizeCurator(context.Context, string, string, bool) (setup.CuratorSession, error) {
	return setup.CuratorSession{PersonID: stub.actor.PersonID, SessionID: stub.actor.SessionID}, stub.curatorErr
}

func TestEngagementRoutesDeclareExplicitPolicies(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(&routeServiceStub{}, routeAuthorizerStub{}))

	routes := e.Routes()
	require.Len(t, routes, 3)
	policies := map[string]string{}
	for _, route := range routes {
		policies[route.Method+" "+route.Path] = route.Name
	}
	assert.Equal(t, recipientEngagementPolicy, policies["POST /api/me/engagement"])
	assert.Equal(t, curatorEngagementPolicy, policies["GET /api/engagement/recipients/:person_id"])
	assert.Equal(t, curatorEngagementPolicy, policies["GET /api/engagement/media/:media_id/openers"])
}

func TestRecipientEngagementRequiresSessionCSRFAndVisibleExplicitClaim(t *testing.T) {
	service := &routeServiceStub{}
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(service, routeAuthorizerStub{
		actor: setup.SessionActor{PersonID: uuid.New(), AccessID: uuid.New(), SessionID: uuid.New()},
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/me/engagement", strings.NewReader(`{
		"kind":"visit","client_claim_id":"`+uuid.NewString()+`","destination":null,
		"event_id":null,"media_item_id":null,"document_visible":true
	}`))
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.Header.Set(setup.CSRFHeader, "csrf")
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque-session"})
	response := httptest.NewRecorder()

	e.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.True(t, service.recorded)
	assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
}

func TestRecipientCannotReadCuratorEngagement(t *testing.T) {
	service := &routeServiceStub{}
	e := echo.New()
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	RegisterRoutes(e, NewHandler(service, routeAuthorizerStub{curatorErr: setup.ErrNotCurator}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/engagement/recipients/"+uuid.NewString(), nil)
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "opaque-session"})
	response := httptest.NewRecorder()

	e.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNotFound, response.Code)
}
