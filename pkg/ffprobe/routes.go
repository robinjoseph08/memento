package ffprobe

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/errorstack"
)

// Diagnostic is the Settings seam for the chapter probe.
type Diagnostic interface {
	Check(context.Context) Status
}

func RegisterRoutes(e *echo.Echo, diagnostic Diagnostic, curatorGuard echo.MiddlewareFunc) {
	e.GET("/api/curator/ffprobe", func(c *echo.Context) error {
		return errorstack.CaptureContext(c.Request().Context(), c.JSON(http.StatusOK, diagnostic.Check(c.Request().Context())))
	}, curatorGuard)
}
