package identity_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type managementIdentity struct {
	fakeIdentity
	detail    identity.PersonDetail
	changeErr error
}

func (m *managementIdentity) GetPerson(context.Context, string, string) (identity.PersonDetail, error) {
	return m.detail, nil
}

func (m *managementIdentity) UpdatePerson(context.Context, string, string, identity.UpdatePersonRequest) (identity.Person, error) {
	return identity.Person{}, m.changeErr
}

func (m *managementIdentity) UnlinkIdentity(context.Context, string, string, string) error {
	return m.changeErr
}

func (m *managementIdentity) CreatePerson(_ context.Context, token string, request identity.CreatePersonRequest) (identity.Person, error) {
	if token != "browser-token" {
		return identity.Person{}, identity.ErrUnauthenticated
	}
	return identity.Person{ID: "created", DisplayName: request.DisplayName}, nil
}

func TestPersonDetailHTTPIncludesSessionsAndRequiresCurator(t *testing.T) {
	t.Parallel()
	expires := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	module := &managementIdentity{
		detail: identity.PersonDetail{
			Person:   identity.Person{ID: "target", UpdateEmail: "alex@example.test", EmailUpdates: true},
			Sessions: []identity.BrowserSession{{ID: "session", IdentityID: "account", Email: "alex@example.test", Device: "Firefox", ExpiresAt: expires}},
		},
	}
	module.claimed = true
	module.session.Person.IsCurator = true
	e := identityHTTP(t, config.NewForTest(), &module.fakeIdentity)
	identity.RegisterRoutes(e, config.NewForTest(), module)
	req := httptest.NewRequest(http.MethodGet, "/api/people/target", nil)
	req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "browser-token"})
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var detail identity.PersonDetail
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &detail))
	assert.Equal(t, module.detail, detail)
	assert.Contains(t, recorder.Body.String(), `"sessions":[`)
	assert.Contains(t, recorder.Body.String(), `"expires_at":"2026-12-01T00:00:00Z"`)
	assert.NotContains(t, recorder.Body.String(), "token")
	module.session.Person.IsCurator = false
	recorder = httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "alex@example.test")
}

func TestPeopleHTTPPreservesSelfChangeFieldErrors(t *testing.T) {
	t.Parallel()
	fields := map[string]string{
		"is_curator":  "Ask another Curator to remove your Curator role.",
		"deactivated": "Ask another Curator to deactivate your access.",
	}
	module := &managementIdentity{changeErr: errcodes.ValidationFields("Check the highlighted fields.", fields)}
	module.claimed = true
	module.session.Person.IsCurator = true
	e := identityHTTP(t, config.NewForTest(), &module.fakeIdentity)
	identity.RegisterRoutes(e, config.NewForTest(), module)
	req := httptest.NewRequest(http.MethodPost, "/api/people/self", strings.NewReader(`{"display_name":"Alex","is_curator":false,"deactivated":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "browser-token"})
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusUnprocessableEntity, recorder.Code, recorder.Body.String())
	var response errcodes.ErrorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "validation_error", response.Error.Code)
	assert.Equal(t, fields, response.Error.Fields)
}

func TestUnlinkHTTPReturnsLastAccountConflictOnBothRoutes(t *testing.T) {
	t.Parallel()
	for _, scenario := range []struct {
		path    string
		curator bool
		status  int
		code    string
	}{
		{"/api/identity/identities/account/unlink", false, 409, "last_account"},
		{"/api/identity/identities/account/unlink", true, 409, "last_account"},
		{"/api/people/self/identities/account/unlink", true, 409, "last_account"},
		{"/api/people/self/identities/account/unlink", false, 403, "access_denied"},
	} {
		module := &managementIdentity{changeErr: identity.ErrLastAccount}
		module.claimed = true
		module.session.Person.IsCurator = scenario.curator
		e := identityHTTP(t, config.NewForTest(), &module.fakeIdentity)
		identity.RegisterRoutes(e, config.NewForTest(), module)
		req := httptest.NewRequest(http.MethodPost, scenario.path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "browser-token"})
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, req)
		require.Equal(t, scenario.status, recorder.Code, recorder.Body.String())
		var response errcodes.ErrorResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		assert.Equal(t, scenario.code, response.Error.Code)
	}
}

func TestPeopleHTTPRequiresCuratorAndValidatesFields(t *testing.T) {
	t.Parallel()
	module := &managementIdentity{}
	module.claimed = true
	module.session.Person.IsCurator = true
	e := identityHTTP(t, config.NewForTest(), &module.fakeIdentity)
	// Register against the management fake while retaining the common test binder.
	identity.RegisterRoutes(e, config.NewForTest(), module)
	for _, test := range []struct {
		body     string
		curator  bool
		want     int
		contains string
	}{
		{`{"display_name":"Alex"}`, true, 200, `"display_name":"Alex"`},
		{`{"display_name":" "}`, true, 422, "Enter a display name."},
		{`{"display_name":"Alex","is_curator":true}`, true, 422, "unknown_parameter"},
		{`{"display_name":"Alex"}`, false, 403, "access_denied"},
	} {
		module.session.Person.IsCurator = test.curator
		req := httptest.NewRequest(http.MethodPost, "/api/people", strings.NewReader(test.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "browser-token"})
		recorder := httptest.NewRecorder()
		e.ServeHTTP(recorder, req)
		assert.Equal(t, test.want, recorder.Code, recorder.Body.String())
		assert.Contains(t, recorder.Body.String(), test.contains)
	}
}
