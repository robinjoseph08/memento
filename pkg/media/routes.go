package media

import "github.com/labstack/echo/v5"

// RegisterRoutes requires the Curator guard for both source and imported media.
func RegisterRoutes(e *echo.Echo, m *Module, requireCurator echo.MiddlewareFunc) {
	h := &handlers{module: m}
	e.GET("/api/media/sources/:id/cover", h.sourceCover, requireCurator)
	e.HEAD("/api/media/sources/:id/cover", h.sourceCover, requireCurator)
	e.GET("/api/media/entries/:id/thumbnail", h.entryThumbnail, requireCurator)
	e.HEAD("/api/media/entries/:id/thumbnail", h.entryThumbnail, requireCurator)
}
