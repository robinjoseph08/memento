package repairs

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type routeAuthorizer struct {
	err       error
	mutations []bool
}

func (authorizer *routeAuthorizer) AuthorizeCurator(_ context.Context, _, _ string, mutation bool) (setup.CuratorSession, error) {
	authorizer.mutations = append(authorizer.mutations, mutation)
	return setup.CuratorSession{}, authorizer.err
}

type routeService struct {
	confirmMedia func(context.Context, setup.CuratorSession, uuid.UUID, string) (MutationResponse, error)
}

func (*routeService) List(context.Context) (ListResponse, error) { return ListResponse{}, nil }
func (*routeService) ReconcilePeople(context.Context) (MutationResponse, error) {
	return MutationResponse{}, nil
}
func (*routeService) LinkPerson(context.Context, setup.CuratorSession, LinkPersonRequest) (MutationResponse, error) {
	return MutationResponse{}, nil
}
func (*routeService) ConfirmPerson(context.Context, setup.CuratorSession, uuid.UUID) (MutationResponse, error) {
	return MutationResponse{}, nil
}
func (*routeService) RejectPerson(context.Context, setup.CuratorSession, uuid.UUID) (MutationResponse, error) {
	return MutationResponse{}, nil
}
func (service *routeService) ConfirmMedia(ctx context.Context, actor setup.CuratorSession, id uuid.UUID, token string) (MutationResponse, error) {
	return service.confirmMedia(ctx, actor, id, token)
}
func (*routeService) RejectMedia(context.Context, setup.CuratorSession, uuid.UUID) (MutationResponse, error) {
	return MutationResponse{}, nil
}

func repairHTTP(authorizer Authorizer) *echo.Echo {
	return repairHTTPWithService(&routeService{confirmMedia: func(context.Context, setup.CuratorSession, uuid.UUID, string) (MutationResponse, error) {
		return MutationResponse{}, nil
	}}, authorizer)
}

func repairHTTPWithService(service handlerService, authorizer Authorizer) *echo.Echo {
	e := echo.New()
	RegisterRoutes(e, &Handler{service: service, authorizer: authorizer})
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}

func repairRequest(e *echo.Echo, method, path, session, csrf string) *httptest.ResponseRecorder {
	return repairJSONRequest(e, method, path, session, csrf, "")
}

func repairJSONRequest(e *echo.Echo, method, path, session, csrf, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
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
		assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
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
		"/api/repairs/media/11111111-1111-4111-8111-111111111111/confirm",
		"/api/repairs/media/11111111-1111-4111-8111-111111111111/reject",
	} {
		authorizer := &routeAuthorizer{err: setup.ErrCSRF}
		response := repairRequest(repairHTTP(authorizer), http.MethodPost, path, "session", "wrong")
		assert.Equal(t, http.StatusForbidden, response.Code, path)
		assert.Equal(t, []bool{true}, authorizer.mutations, path)
		assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
		assert.NotContains(t, response.Body.String(), "wrong")
	}
}

func TestMediaConfirmHTTPRouteBindsReviewTokenAndMapsConflicts(t *testing.T) {
	candidateID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	reviewToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	path := "/api/repairs/media/" + candidateID.String() + "/confirm"

	t.Run("JSON review token", func(t *testing.T) {
		called := false
		service := &routeService{confirmMedia: func(_ context.Context, _ setup.CuratorSession, id uuid.UUID, token string) (MutationResponse, error) {
			called = true
			assert.Equal(t, candidateID, id)
			assert.Equal(t, reviewToken, token)
			return MutationResponse{Status: "confirmed"}, nil
		}}
		response := repairJSONRequest(repairHTTPWithService(service, &routeAuthorizer{}), http.MethodPost,
			path, "session", "csrf", `{"review_token":"`+reviewToken+`"}`)
		require.Equal(t, http.StatusOK, response.Code)
		assert.True(t, called)
		assert.JSONEq(t, `{"status":"confirmed"}`, response.Body.String())
	})

	t.Run("missing review token", func(t *testing.T) {
		service := &routeService{confirmMedia: func(_ context.Context, _ setup.CuratorSession, _ uuid.UUID, token string) (MutationResponse, error) {
			assert.Empty(t, token)
			return MutationResponse{}, ErrConflict
		}}
		response := repairJSONRequest(repairHTTPWithService(service, &routeAuthorizer{}), http.MethodPost,
			path, "session", "csrf", `{}`)
		assert.Equal(t, http.StatusConflict, response.Code)
		assert.Contains(t, response.Body.String(), "Repair confirmation could not be completed")
		assert.NotContains(t, response.Body.String(), "evidence changed")
		assert.NotContains(t, response.Body.String(), "another confirmed link")
	})

	t.Run("domain conflict", func(t *testing.T) {
		service := &routeService{confirmMedia: func(context.Context, setup.CuratorSession, uuid.UUID, string) (MutationResponse, error) {
			return MutationResponse{}, ErrConflict
		}}
		response := repairJSONRequest(repairHTTPWithService(service, &routeAuthorizer{}), http.MethodPost,
			path, "session", "csrf", `{"review_token":"`+reviewToken+`"}`)
		assert.Equal(t, http.StatusConflict, response.Code)
		assert.Contains(t, response.Body.String(), "Repair confirmation could not be completed")
		assert.NotContains(t, response.Body.String(), "evidence changed")
		assert.NotContains(t, response.Body.String(), "another confirmed link")
		assert.Equal(t, "no-store", response.Header().Get(echo.HeaderCacheControl))
	})
}
