package immich

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

type Diagnostic interface {
	Check(context.Context) Connection
}

func RegisterRoutes(e *echo.Echo, diagnostic Diagnostic, setupGuard, curatorGuard echo.MiddlewareFunc) {
	handler := func(c *echo.Context) error {
		return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, diagnostic.Check(c.Request().Context())))
	}
	e.GET("/api/setup/connection", handler, setupGuard)
	e.GET("/api/curator/connection", handler, curatorGuard)
}
