package favorites

import (
	"context"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/stretchr/testify/assert"
)

type authorizerStub struct{}

func (authorizerStub) AuthorizeSession(context.Context, string, string, bool) (setup.SessionActor, error) {
	return setup.SessionActor{}, nil
}

func TestRegisterRoutesSeparatesRecipientAndCuratorFavoritePolicies(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(nil, authorizerStub{}))
	routes := map[string]string{}
	for _, route := range e.Routes() {
		routes[route.Method+" "+route.Path] = route.Name
	}
	assert.Equal(t, "policy:recipient_content", routes["GET /api/favorites/:media_id"])
	assert.Equal(t, "policy:recipient_content_csrf", routes["PUT /api/favorites/:media_id"])
	assert.Equal(t, "policy:recipient_content_csrf", routes["DELETE /api/favorites/:media_id"])
	assert.Equal(t, "policy:curator", routes["GET /api/favorites/curator/recipients/:recipient_id"])
}
