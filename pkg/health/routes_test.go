package health

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func TestRoutesDeclarePublicSafePolicy(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, new(Service))

	policies := make(map[string]string)
	for _, route := range e.Routes() {
		policies[route.Path] = route.Name
	}
	assert.Equal(t, publicSafePolicy, policies["/api/health/live"])
	assert.Equal(t, publicSafePolicy, policies["/api/health/ready"])
}
