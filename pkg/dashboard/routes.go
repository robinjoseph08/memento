package dashboard

import "github.com/labstack/echo/v5"

// RegisterRoutes keeps the overview behind the Curator guard.
func RegisterRoutes(e *echo.Echo, module UseCases, requireCurator echo.MiddlewareFunc) {
	h := &handlers{module: module}
	e.GET("/api/curator/dashboard", h.overview, requireCurator)
}
