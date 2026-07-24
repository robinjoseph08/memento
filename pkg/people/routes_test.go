package people

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutesDeclareExplicitCuratorPolicies(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(nil, nil))
	policies := make(map[string]string)
	for _, route := range e.Routes() {
		policies[route.Method+" "+route.Path] = route.Name
	}
	assert.Equal(t, curatorReadPolicy, policies["GET /api/people"])
	assert.Equal(t, curatorReadPolicy, policies["GET /api/people/:id"])
	for _, route := range []string{
		"POST /api/people", "PATCH /api/people/:id", "POST /api/people/:id/archive",
		"POST /api/people/merge-preview", "POST /api/people/merge",
	} {
		assert.Equal(t, curatorMutationPolicy, policies[route], route)
	}
}

func TestNamesTrimAndDefaultSortName(t *testing.T) {
	displayName, sortName, err := names("  Robin Joseph  ", "")
	require.NoError(t, err)
	assert.Equal(t, "Robin Joseph", displayName)
	assert.Equal(t, displayName, sortName)

	_, _, err = names("", "Sort")
	require.ErrorIs(t, err, ErrInvalidPerson)
	_, _, err = names("Person", string(make([]byte, maxNameLength+1)))
	require.ErrorIs(t, err, ErrInvalidPerson)
}
