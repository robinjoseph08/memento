package emaildelivery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/stretchr/testify/assert"
)

type routeStub struct{}

func (routeStub) RequestTest(c echo.Context) error { return c.NoContent(http.StatusAccepted) }
func (routeStub) GetStatus(c echo.Context) error   { return c.NoContent(http.StatusOK) }

func TestDisabledSMTPReturnsSafeServiceUnavailable(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, NewHandler(New(nil, config.SMTPConfig{}, nil)))
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/setup/email/test", nil)
	response := httptest.NewRecorder()

	e.ServeHTTP(response, request)

	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"service_unavailable"`)
	assert.NotContains(t, response.Body.String(), "password")
}

func TestRoutesDeclareSetupOnlyPolicy(t *testing.T) {
	e := echo.New()
	RegisterRoutes(e, routeStub{})

	policies := make(map[string]string)
	for _, route := range e.Routes() {
		policies[route.Path] = route.Name
	}
	assert.Equal(t, setupOnlyPolicy, policies["/api/setup/email/test"])
	assert.Equal(t, setupOnlyPolicy, policies["/api/setup/email/test/:delivery_id"])
}
