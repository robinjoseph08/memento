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
	"github.com/stretchr/testify/require"
)

// circleIdentity records which Circle use case a route reached and with what.
type circleIdentity struct {
	fakeIdentity
	called string
	id     string
	name   string
	ids    []string
}

func (m *circleIdentity) ListCircles(context.Context, string) ([]identity.Circle, error) {
	m.called = "list"
	return []identity.Circle{{ID: "circle", Name: "Extended family", Members: []identity.Person{}}}, nil
}

func (m *circleIdentity) CreateCircle(_ context.Context, _ string, request identity.CircleRequest) (identity.Circle, error) {
	m.called, m.name = "create", request.Name
	return identity.Circle{ID: "circle", Name: request.Name, Members: []identity.Person{}}, nil
}

func (m *circleIdentity) RenameCircle(_ context.Context, _, id string, request identity.CircleRequest) (identity.Circle, error) {
	m.called, m.id, m.name = "rename", id, request.Name
	return identity.Circle{ID: id, Name: request.Name, Members: []identity.Person{}}, nil
}

func (m *circleIdentity) DeleteCircle(_ context.Context, _, id string) error {
	m.called, m.id = "delete", id
	return nil
}

func (m *circleIdentity) SetCircleMembers(_ context.Context, _, id string, request identity.CircleMembersRequest) (identity.Circle, error) {
	m.called, m.id, m.ids = "members", id, request.PersonIDs
	return identity.Circle{ID: id, Name: "Extended family", Members: []identity.Person{}}, nil
}

func (m *circleIdentity) SetPersonCircles(_ context.Context, _, id string, request identity.PersonCirclesRequest) error {
	m.called, m.id, m.ids = "person", id, request.CircleIDs
	return nil
}

func TestCircleHTTPRoutesRequireCurator(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		method, path, body string
		status             int
		called, id, name   string
		ids                []string
		contains           string
	}{
		{method: http.MethodGet, path: "/api/circles", status: 200, called: "list", contains: `"name":"Extended family"`},
		{method: http.MethodPost, path: "/api/circles", body: `{"name":" College friends "}`, status: 200, called: "create", name: "College friends"},
		{method: http.MethodPost, path: "/api/circles", body: `{"name":" "}`, status: 422, contains: "Enter a Circle name."},
		{method: http.MethodPost, path: "/api/circles/circle", body: `{"name":"Old friends"}`, status: 200, called: "rename", id: "circle", name: "Old friends"},
		{method: http.MethodPost, path: "/api/circles/circle/delete", body: `{}`, status: 204, called: "delete", id: "circle"},
		{method: http.MethodPost, path: "/api/circles/circle/members", body: `{"person_ids":["alex","sam"]}`, status: 200, called: "members", id: "circle", ids: []string{"alex", "sam"}},
		{method: http.MethodPost, path: "/api/circles/circle/members", body: `{}`, status: 422, contains: "Refresh the page and choose people from the list."},
		{method: http.MethodPost, path: "/api/people/alex/circles", body: `{"circle_ids":[]}`, status: 204, called: "person", id: "alex", ids: []string{}},
		{method: http.MethodPost, path: "/api/people/alex/circles", body: `{}`, status: 422, contains: "Refresh the page and choose Circles from the list."},
	} {
		for _, curator := range []bool{true, false} {
			module := &circleIdentity{}
			module.claimed = true
			module.session.Person.IsCurator = curator
			e := identityHTTP(t, config.NewForTest(), &module.fakeIdentity)
			identity.RegisterRoutes(e, config.NewForTest(), module)
			req := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: "browser-token"})
			recorder := httptest.NewRecorder()
			e.ServeHTTP(recorder, req)
			if !curator {
				assert.Equal(t, http.StatusForbidden, recorder.Code, test.path)
				assert.Empty(t, module.called, test.path)
				assert.NotContains(t, recorder.Body.String(), "Extended family")
				continue
			}
			require.Equal(t, test.status, recorder.Code, "%s %s: %s", test.method, test.path, recorder.Body.String())
			assert.Equal(t, test.called, module.called, test.path)
			assert.Equal(t, test.id, module.id, test.path)
			assert.Equal(t, test.name, module.name, test.path)
			assert.Equal(t, test.ids, module.ids, test.path)
			assert.Contains(t, recorder.Body.String(), test.contains)
		}
	}
}
