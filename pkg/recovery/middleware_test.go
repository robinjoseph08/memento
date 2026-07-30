package recovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
)

func TestReadinessOwnsDependencyFailureReportingWithoutRecoveryFence(t *testing.T) {
	e := echo.New()
	e.Use(middleware.Recover())
	e.Use((&Service{}).Middleware())
	e.GET("/api/health/ready", func(c echo.Context) error {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"postgresql": "unavailable"})
	})

	response := httptest.NewRecorder()
	e.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/health/ready", nil))

	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.JSONEq(t, `{"postgresql":"unavailable"}`, response.Body.String())
}
