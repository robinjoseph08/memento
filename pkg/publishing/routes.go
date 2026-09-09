package publishing

import "github.com/labstack/echo/v5"

func RegisterRoutes(e *echo.Echo, module *Module, requireCurator echo.MiddlewareFunc) {
	h := &handlers{module: module}
	e.GET("/api/curator/sources", h.sources, requireCurator)
	e.POST("/api/curator/imports", h.startImport, requireCurator)
	e.GET("/api/curator/albums", h.albums, requireCurator)
	e.GET("/api/curator/albums/:id", h.album, requireCurator)
	e.POST("/api/curator/albums/:id", h.updateAlbum, requireCurator)
	e.POST("/api/curator/albums/:id/retry", h.retryImport, requireCurator)
}
