package publishing

import "github.com/labstack/echo/v5"

// RegisterViewerRoutes keeps selected-Person preview behind the Curator guard.
func RegisterViewerRoutes(e *echo.Echo, module ViewerUseCases, requirePerson, requireCurator echo.MiddlewareFunc) {
	h := &viewerHandlers{module: module}
	e.GET("/api/albums", h.albums, requirePerson)
	e.GET("/api/albums/:id", h.album, requirePerson)
	e.GET("/api/albums/:id/photos", h.photos, requirePerson)
	e.GET("/api/albums/:id/videos", h.videos, requirePerson)
	e.GET("/api/curator/albums/:id/preview/:personID", h.album, requireCurator)
	e.GET("/api/curator/albums/:id/preview/:personID/photos", h.photos, requireCurator)
	e.GET("/api/curator/albums/:id/preview/:personID/videos", h.videos, requireCurator)
}

// RegisterAccessRoutes keeps every access mutation behind the Curator guard.
func RegisterAccessRoutes(e *echo.Echo, module AccessUseCases, requireCurator echo.MiddlewareFunc) {
	h := &accessHandlers{module: module}
	e.POST("/api/curator/albums/:id/access", h.setAlbumAccess, requireCurator)
	e.POST("/api/curator/albums/:id/access/undo", h.undoAlbumAccess, requireCurator)
	e.POST("/api/curator/albums/:id/access/remove-all/preview", h.previewRemoveAccess, requireCurator)
	e.POST("/api/curator/albums/:id/access/remove-all", h.removeAccess, requireCurator)
	e.POST("/api/curator/albums/:id/entries/:entryID/access", h.setEntryAccess, requireCurator)
	e.POST("/api/curator/albums/:id/entries/:entryID/access/undo", h.undoEntryAccess, requireCurator)
	e.POST("/api/curator/albums/:id/entries/:entryID/rules", h.saveEntryRules, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/rules", h.saveMomentRules, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/access", h.setMomentAccess, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/access/suggestions", h.addMomentSuggestions, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/access/undo", h.undoMomentAccess, requireCurator)
}

func RegisterPublicationRoutes(e *echo.Echo, module PublicationUseCases, requireCurator echo.MiddlewareFunc) {
	h := &publicationHandlers{module: module}
	e.GET("/api/curator/albums/:id/publication", h.publication, requireCurator)
	e.POST("/api/curator/albums/:id/publish", h.publish, requireCurator)
	e.POST("/api/curator/albums/:id/unpublish", h.unpublish, requireCurator)
	e.POST("/api/curator/albums/:id/delete", h.deleteAlbum, requireCurator)
}

func RegisterRoutes(e *echo.Echo, module *Module, requireCurator echo.MiddlewareFunc) {
	RegisterAccessRoutes(e, module, requireCurator)
	RegisterPublicationRoutes(e, module, requireCurator)
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
	e.POST("/api/curator/albums/:id/moments/:momentID/move/preview", h.previewMove, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/move", h.moveEntries, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/split/preview", h.previewSplit, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/split", h.splitMoment, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/merge/preview", h.previewMerge, requireCurator)
	e.POST("/api/curator/albums/:id/moments/:momentID/merge", h.mergeMoments, requireCurator)
}
