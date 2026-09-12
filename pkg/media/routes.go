package media

import "github.com/labstack/echo/v5"

// RegisterViewerRoutes scopes browser caches to the authenticated Person or an
// explicit Curator preview. Preview only authorizes generated thumbnails.
func RegisterViewerRoutes(e *echo.Echo, m *Module, authorize AuthorizeEntry, requirePerson, requireCurator echo.MiddlewareFunc) {
	h := &viewerHandlers{module: m, authorize: authorize}
	e.GET("/api/media/viewer/:personID/entries/:id/thumbnail", h.entryThumbnail, requirePerson)
	e.HEAD("/api/media/viewer/:personID/entries/:id/thumbnail", h.entryThumbnail, requirePerson)
	e.GET("/api/media/preview/:personID/entries/:id/thumbnail", h.previewThumbnail, requireCurator)
	e.HEAD("/api/media/preview/:personID/entries/:id/thumbnail", h.previewThumbnail, requireCurator)
}

// RegisterRoutes requires the Curator guard for both source and imported media.
func RegisterRoutes(e *echo.Echo, m *Module, requireCurator echo.MiddlewareFunc) {
	h := &handlers{module: m}
	e.GET("/api/media/sources/:id/cover", h.sourceCover, requireCurator)
	e.HEAD("/api/media/sources/:id/cover", h.sourceCover, requireCurator)
	e.GET("/api/media/entries/:id/thumbnail", h.entryThumbnail, requireCurator)
	e.HEAD("/api/media/entries/:id/thumbnail", h.entryThumbnail, requireCurator)
	e.GET("/api/media/faces/:sourceID/thumbnail", h.faceThumbnail, requireCurator)
	e.HEAD("/api/media/faces/:sourceID/thumbnail", h.faceThumbnail, requireCurator)
	e.GET("/api/media/people/:id/avatar", h.personAvatar, requireCurator)
	e.HEAD("/api/media/people/:id/avatar", h.personAvatar, requireCurator)
}
