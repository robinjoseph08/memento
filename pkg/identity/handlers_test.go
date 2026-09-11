package identity_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeIdentity struct {
	identity.UseCases
	claimed       bool
	session       identity.Session
	expectedToken string
	err           error
	faceAction    string
	personID      string
	sourceFaceID  string
}

func (f *fakeIdentity) Claimed(context.Context) (bool, error) { return f.claimed, nil }
func (f *fakeIdentity) SignIn(context.Context, identity.Claims) (identity.Session, error) {
	return f.session, f.err
}
func (f *fakeIdentity) Authenticate(_ context.Context, token string) (identity.Session, error) {
	if f.expectedToken != "" && token != f.expectedToken {
		return identity.Session{}, identity.ErrUnauthenticated
	}
	return f.session, f.err
}
func (f *fakeIdentity) SignOut(context.Context, string) error { return f.err }
func (f *fakeIdentity) LinkFace(_ context.Context, _ string, personID string, request identity.LinkFaceRequest) (identity.PersonDetail, error) {
	f.faceAction, f.personID, f.sourceFaceID = "link", personID, request.SourceFaceID
	return identity.PersonDetail{}, f.err
}
func (f *fakeIdentity) CreatePersonFromFace(_ context.Context, _ string, request identity.CreatePersonFromFaceRequest) (identity.PersonDetail, error) {
	f.faceAction, f.sourceFaceID = "create", request.SourceFaceID
	return identity.PersonDetail{}, f.err
}
func (f *fakeIdentity) IgnoreFace(_ context.Context, _ string, sourceID string) error {
	f.faceAction, f.sourceFaceID = "ignore", sourceID
	return f.err
}
func (f *fakeIdentity) SetPersonAvatar(_ context.Context, _ string, personID string, request identity.SetPersonAvatarRequest) (identity.PersonDetail, error) {
	f.faceAction, f.personID, f.sourceFaceID = "avatar", personID, request.SourceFaceID
	return identity.PersonDetail{}, f.err
}

func identityHTTP(t *testing.T, cfg *config.Config, module *fakeIdentity) *echo.Echo {
	t.Helper()
	e := echo.New()
	b, err := binder.New()
	require.NoError(t, err)
	e.Binder = b
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	h := identity.RegisterRoutes(e, cfg, module)
	e.GET("/protected", func(c *echo.Context) error { return c.NoContent(204) }, h.RequireCurator)
	e.GET("/setup-only", func(c *echo.Context) error { return c.NoContent(204) }, h.RequireSetupOrCurator)
	return e
}

func TestFaceHTTPRoutes(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		path       string
		body       string
		status     int
		action     string
		personID   string
		sourceFace string
	}{
		"link": {
			path:       "/api/people/person-id/faces",
			body:       `{"source_face_id":"source-face"}`,
			status:     http.StatusOK,
			action:     "link",
			personID:   "person-id",
			sourceFace: "source-face",
		},
		"create": {
			path:       "/api/people/from-face",
			body:       `{"display_name":"Alex","source_face_id":"source-face"}`,
			status:     http.StatusOK,
			action:     "create",
			sourceFace: "source-face",
		},
		"avatar": {
			path:       "/api/people/person-id/avatar",
			body:       `{"source_face_id":"source-face"}`,
			status:     http.StatusOK,
			action:     "avatar",
			personID:   "person-id",
			sourceFace: "source-face",
		},
		"ignore": {
			path:       "/api/faces/source-face/ignore",
			body:       `{}`,
			status:     http.StatusNoContent,
			action:     "ignore",
			sourceFace: "source-face",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := config.NewForTest()
			module := &fakeIdentity{claimed: true, session: identity.Session{Person: identity.Person{IsCurator: true}, Token: "token"}}
			e := identityHTTP(t, cfg, module)
			req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: cfg.CookieNamespace + "_session", Value: "token"})
			recorder := httptest.NewRecorder()
			e.ServeHTTP(recorder, req)
			require.Equal(t, test.status, recorder.Code, recorder.Body.String())
			assert.Equal(t, test.action, module.faceAction)
			assert.Equal(t, test.personID, module.personID)
			assert.Equal(t, test.sourceFace, module.sourceFaceID)
		})
	}
}

func TestIdentityHTTPTranslation(t *testing.T) {
	t.Parallel()
	cfg := config.NewForTest()
	cfg.PublicURL = "https://photos.example.test"
	session := identity.Session{Person: identity.Person{ID: "person", DisplayName: "Alex", IsCurator: true}, Token: strings.Repeat("x", 43), ExpiresAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}
	module := &fakeIdentity{session: session}
	e := identityHTTP(t, cfg, module)
	req := httptest.NewRequest(http.MethodPost, "/api/identity/fake-sign-in", strings.NewReader(`{"email":"owner@example.test","display_name":"Alex"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "http")
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	require.Equal(t, 200, recorder.Code, recorder.Body.String())
	cookies := recorder.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure)
	assert.True(t, cookies[0].HttpOnly)
	assert.Equal(t, http.SameSiteLaxMode, cookies[0].SameSite)
	assert.Equal(t, "/", cookies[0].Path)
	assert.Empty(t, cookies[0].Domain)
	var person identity.Person
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &person))
	assert.Equal(t, session.Person, person)
	module.claimed = true
	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 401, recorder.Code)
	req.AddCookie(cookies[0])
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 204, recorder.Code)
	assert.Empty(t, recorder.Result().Cookies(), "unchanged sessions must not overwrite browser cookies")
	module.session.Renewed = true
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 204, recorder.Code)
	require.Len(t, recorder.Result().Cookies(), 1)
	assert.Equal(t, session.ExpiresAt, recorder.Result().Cookies()[0].Expires)
	module.session.Person.IsCurator = false
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 403, recorder.Code)
	req = httptest.NewRequest(http.MethodGet, "/setup-only", nil)
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 401, recorder.Code)
	module.claimed = false
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 204, recorder.Code)
	req = httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.AddCookie(cookies[0])
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 409, recorder.Code)
}

func TestDevelopmentInstancesKeepIndependentBrowserSessions(t *testing.T) {
	t.Parallel()

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}
	servers := make([]*httptest.Server, 0, 2)
	for _, instance := range []struct {
		namespace string
		token     string
	}{
		{namespace: "memento_first_worktree", token: strings.Repeat("a", 43)},
		{namespace: "memento_second_worktree", token: strings.Repeat("b", 43)},
	} {
		cfg := config.NewForTest()
		cfg.CookieNamespace = instance.namespace
		module := &fakeIdentity{
			claimed:       true,
			expectedToken: instance.token,
			session: identity.Session{
				Person:    identity.Person{ID: instance.namespace, DisplayName: instance.namespace, IsCurator: true},
				Token:     instance.token,
				ExpiresAt: time.Now().Add(time.Hour),
			},
		}
		server := httptest.NewServer(identityHTTP(t, cfg, module))
		t.Cleanup(server.Close)
		servers = append(servers, server)

		response, err := client.Post(server.URL+"/api/identity/fake-sign-in", "application/json", strings.NewReader(`{"email":"owner@example.test","display_name":"Owner"}`))
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, http.StatusOK, response.StatusCode)
	}

	for _, server := range servers {
		response, err := client.Get(server.URL + "/protected")
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		assert.Equal(t, http.StatusNoContent, response.StatusCode)
	}
}

func TestFakeSignInRejectsSubjectOverride(t *testing.T) {
	t.Parallel()
	e := identityHTTP(t, config.NewForTest(), &fakeIdentity{})
	req := httptest.NewRequest(http.MethodPost, "/api/identity/fake-sign-in", strings.NewReader(`{"email":"owner@example.test","display_name":"Alex","subject":"another-identity"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, 422, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "unknown_parameter")
}

func TestSignInActionableFieldErrors(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		request string
		fields  map[string]string
	}{
		"required fields after trimming": {
			request: `{"email":"  ","display_name":"  "}`,
			fields: map[string]string{
				"email":        "Enter an email address.",
				"display_name": "Enter a display name.",
			},
		},
		"single missing display name": {
			request: `{"email":"owner@example.test","display_name":""}`,
			fields: map[string]string{
				"display_name": "Enter a display name.",
			},
		},
		"shared defaults for other rules": {
			request: `{"email":"invalid","display_name":"` + strings.Repeat("é", 101) + `"}`,
			fields: map[string]string{
				"email":        "Enter a valid email address.",
				"display_name": "Use 100 characters or fewer.",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e := identityHTTP(t, config.NewForTest(), &fakeIdentity{})
			req := httptest.NewRequest(http.MethodPost, "/api/identity/fake-sign-in", strings.NewReader(test.request))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			e.ServeHTTP(recorder, req)
			require.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
			var body errcodes.ErrorResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			assert.Equal(t, "validation_error", body.Error.Code)
			assert.Equal(t, "Check the highlighted fields.", body.Error.Message)
			assert.Equal(t, test.fields, body.Error.Fields)
			assert.Empty(t, recorder.Result().Cookies())
		})
	}
}

func TestSignInFieldErrorsAndSignOut(t *testing.T) {
	t.Parallel()
	module := &fakeIdentity{}
	e := identityHTTP(t, config.NewForTest(), module)
	req := httptest.NewRequest(http.MethodPost, "/api/identity/fake-sign-in", strings.NewReader(`{"email":"invalid","display_name":""}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	require.Equal(t, 422, recorder.Code)
	var body errcodes.ErrorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Len(t, body.Error.Fields, 2)
	req = httptest.NewRequest(http.MethodPost, "/api/identity/sign-out", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	require.Equal(t, 204, recorder.Code)
	require.Len(t, recorder.Result().Cookies(), 1)
	assert.Equal(t, -1, recorder.Result().Cookies()[0].MaxAge)
}
