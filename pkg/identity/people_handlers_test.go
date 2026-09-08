package identity_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/stretchr/testify/assert"
)

type managementIdentity struct{ fakeIdentity }

func (m *managementIdentity) CreatePerson(_ context.Context, token string, request identity.CreatePersonRequest) (identity.Person, error) {
	if token != "browser-token" {
		return identity.Person{}, identity.ErrUnauthenticated
	}
	return identity.Person{ID: "created", DisplayName: request.DisplayName}, nil
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
