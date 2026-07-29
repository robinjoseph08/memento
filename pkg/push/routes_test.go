package push

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func TestRegisterRoutesDeclaresPushPolicies(t *testing.T) {
	t.Parallel()
	e := echo.New()
	RegisterRoutes(e, &Handler{})
	policies := make(map[string]string)
	for _, route := range e.Routes() {
		if route.Method != "echo_route_not_found" {
			policies[route.Method+" "+route.Path] = route.Name
		}
	}
	assert.Equal(t, map[string]string{
		"GET /api/push":            selfReadPolicy,
		"POST /api/push":           selfMutationPolicy,
		"POST /api/push/reconcile": selfMutationPolicy,
		"DELETE /api/push":         selfMutationPolicy,
	}, policies)
}
