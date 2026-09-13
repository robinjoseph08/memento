package media

import "github.com/labstack/echo/v5"

// RegisterViewerRoutes scopes browser caches to the authenticated Person or an
// explicit Curator preview. The selected Person is part of every URL, so one
// browser can cache several previews without mixing them.
func RegisterViewerRoutes(e *echo.Echo, m *Module, authorize AuthorizeEntry, requirePerson, requireCurator echo.MiddlewareFunc) {
	h := &viewerHandlers{module: m, authorize: authorize}
	e.GET("/api/media/viewer/:personID/entries/:id/thumbnail", h.entryThumbnail, requirePerson)
	e.HEAD("/api/media/viewer/:personID/entries/:id/thumbnail", h.entryThumbnail, requirePerson)
	e.GET("/api/media/viewer/:personID/entries/:id/preview", h.entryPreview, requirePerson)
	e.HEAD("/api/media/viewer/:personID/entries/:id/preview", h.entryPreview, requirePerson)
	e.GET("/api/media/viewer/:personID/entries/:id/original", h.entryOriginal, requirePerson)
	e.HEAD("/api/media/viewer/:personID/entries/:id/original", h.entryOriginal, requirePerson)
	e.GET("/api/media/viewer/:personID/entries/:id/playback", h.entryPlayback, requirePerson)
	e.HEAD("/api/media/viewer/:personID/entries/:id/playback", h.entryPlayback, requirePerson)
	e.GET("/api/media/preview/:personID/entries/:id/thumbnail", h.previewThumbnail, requireCurator)
	e.HEAD("/api/media/preview/:personID/entries/:id/thumbnail", h.previewThumbnail, requireCurator)
	e.GET("/api/media/preview/:personID/entries/:id/preview", h.previewPreview, requireCurator)
	e.HEAD("/api/media/preview/:personID/entries/:id/preview", h.previewPreview, requireCurator)
	e.GET("/api/media/preview/:personID/entries/:id/playback", h.previewPlayback, requireCurator)
	e.HEAD("/api/media/preview/:personID/entries/:id/playback", h.previewPlayback, requireCurator)
}

// RegisterRoutes requires the Curator guard for source and imported media.
// Avatars need only a signed-in Person: everyone reads their own, Curators
// read everyone's.
func RegisterRoutes(e *echo.Echo, m *Module, requirePerson, requireCurator echo.MiddlewareFunc) {
	h := &handlers{module: m}
	e.GET("/api/media/sources/:id/cover", h.sourceCover, requireCurator)
	e.HEAD("/api/media/sources/:id/cover", h.sourceCover, requireCurator)
	e.GET("/api/media/entries/:id/thumbnail", h.entryThumbnail, requireCurator)
	e.HEAD("/api/media/entries/:id/thumbnail", h.entryThumbnail, requireCurator)
	e.GET("/api/media/entries/:id/playback", h.entryPlayback, requireCurator)
	e.HEAD("/api/media/entries/:id/playback", h.entryPlayback, requireCurator)
	e.GET("/api/media/faces/:sourceID/thumbnail", h.faceThumbnail, requireCurator)
	e.HEAD("/api/media/faces/:sourceID/thumbnail", h.faceThumbnail, requireCurator)
	e.GET("/api/media/people/:id/avatar", h.personAvatar, requirePerson)
	e.HEAD("/api/media/people/:id/avatar", h.personAvatar, requirePerson)
}
