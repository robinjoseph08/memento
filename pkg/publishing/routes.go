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
	e.POST("/api/curator/albums/:id/moments/:momentID", h.updateMoment, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/cover", h.setMomentCover, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/faces/refresh", h.refreshMomentFaces, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/access", h.setMomentAccess, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/access/suggestions", h.addMomentSuggestions, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/access/undo", h.undoMomentAccess, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/move/preview", h.previewMove, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/move", h.moveEntries, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/split/preview", h.previewSplit, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/split", h.splitMoment, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/merge/preview", h.previewMerge, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/merge", h.mergeMoments, requireCurator)
}
