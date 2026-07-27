package audiences

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type audienceAuthorizer struct {
	actor    setup.CuratorSession
	err      error
	mutation bool
}

func (authorizer *audienceAuthorizer) AuthorizeCurator(_ context.Context, _, _ string, mutation bool) (setup.CuratorSession, error) {
	authorizer.mutation = mutation
	return authorizer.actor, authorizer.err
}
func (*audienceAuthorizer) ContextWithRequestMetadata(ctx context.Context, _ *http.Request) context.Context {
	return ctx
}

func audienceHTTP(authorizer Authorizer) *echo.Echo {
	e := echo.New()
	requestBinder, err := binder.New()
	if err != nil {
		panic(err)
	}
	e.Binder = requestBinder
	RegisterRoutes(e, NewHandler(nil, authorizer))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}
func audienceRequest(e *echo.Echo, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "session"})
	request.Header.Set(setup.CSRFHeader, "csrf")
	request.Header.Set("If-Match", "1")
	if body != "" {
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func TestAudienceRoutesAreInvisibleOutsideCuratorSessions(t *testing.T) {
	e := audienceHTTP(&audienceAuthorizer{err: setup.ErrNotCurator})
	id := "11111111-1111-4111-8111-111111111111"
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/moments/" + id + "/attendance-audience", ""},
		{http.MethodPut, "/api/moments/" + id + "/attendance", `{"person_ids":[]}`},
		{http.MethodPut, "/api/moments/" + id + "/audience/override", `{"recipient_person_id":"` + id + `","state":"included"}`},
		{http.MethodPost, "/api/moments/" + id + "/audience/approve", ""},
		{http.MethodGet, "/api/loose-items/" + id + "/attendance-audience", ""},
	} {
		response := audienceRequest(e, request.method, request.path, request.body)
		assert.Equal(t, http.StatusNotFound, response.Code)
		assert.NotContains(t, response.Body.String(), "face")
	}
}

func TestAudienceMutationsRequireCSRFBeforeBindingOrServiceAccess(t *testing.T) {
	authorizer := &audienceAuthorizer{err: setup.ErrCSRF}
	e := audienceHTTP(authorizer)
	id := "11111111-1111-4111-8111-111111111111"
	for _, request := range []struct{ method, path string }{
		{http.MethodPut, "/api/moments/" + id + "/attendance"},
		{http.MethodPut, "/api/moments/" + id + "/audience/override"},
		{http.MethodPost, "/api/moments/" + id + "/audience/approve"},
		{http.MethodPost, "/api/loose-items/" + id + "/audience/approve"},
	} {
		response := audienceRequest(e, request.method, request.path, `{}`)
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.Contains(t, response.Body.String(), "valid CSRF token")
		assert.True(t, authorizer.mutation)
	}
}

func TestAudienceRoutesValidateBodiesAndUseNoStore(t *testing.T) {
	e := audienceHTTP(&audienceAuthorizer{})
	id := "11111111-1111-4111-8111-111111111111"
	response := audienceRequest(e, http.MethodPut, "/api/moments/"+id+"/audience/override", `{}`)
	require.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Contains(t, response.Body.String(), "recipient_person_id")
	assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
	response = audienceRequest(e, http.MethodGet, "/api/moments/not-an-id/attendance-audience", "")
	assert.Equal(t, http.StatusNotFound, response.Code)
	assert.Contains(t, response.Body.String(), "Audience review not found")

	request := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/moments/"+id+"/attendance", strings.NewReader(`{"person_ids":[]}`))
	request.AddCookie(&http.Cookie{Name: setup.CookieName, Value: "session"})
	request.Header.Set(setup.CSRFHeader, "csrf")
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	missingVersion := httptest.NewRecorder()
	e.ServeHTTP(missingVersion, request)
	assert.Equal(t, http.StatusUnprocessableEntity, missingVersion.Code)
	assert.Contains(t, missingVersion.Body.String(), "If-Match")
}
