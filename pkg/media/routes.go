package media

import (
	"strings"

	"github.com/labstack/echo/v5"
)

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

// RegisterSignedRoutes lets a signed-in Person mint a media URL that a TV can
// fetch without the session cookie, and serves those URLs. publicURL roots
// them, because the TV needs a full address. The serving routes sit behind no
// guard: the token names the Person, and authorize runs for that Person on
// every request, so removing access stops an unexpired URL.
func RegisterSignedRoutes(e *echo.Echo, m *Module, authorize AuthorizeEntry, publicURL string, requirePerson echo.MiddlewareFunc) {
	h := &signedHandlers{module: m, authorize: authorize, publicURL: strings.TrimRight(publicURL, "/")}
	e.POST("/api/media/signed", h.sign, requirePerson)
	e.GET("/api/media/signed/:variant/:token", h.serve)
	e.HEAD("/api/media/signed/:variant/:token", h.serve)
}

// RegisterRoutes requires the Curator guard for source and imported media.
// Avatars need only a signed-in Person: everyone reads their own, Curators
// read everyone's.
func RegisterRoutes(e *echo.Echo, m *Module, requirePerson, requireCurator echo.MiddlewareFunc) {
	h := &handlers{module: m}
	e.GET("/api/media/sources/:id/cover", h.sourceCover, requireCurator)
	e.HEAD("/api/media/sources/:id/cover", h.sourceCover, requireCurator)
	e.GET("/api/media/sources/assets/:id/thumbnail", h.sourceAssetThumbnail, requireCurator)
	e.HEAD("/api/media/sources/assets/:id/thumbnail", h.sourceAssetThumbnail, requireCurator)
	e.GET("/api/media/entries/:id/thumbnail", h.entryThumbnail, requireCurator)
	e.HEAD("/api/media/entries/:id/thumbnail", h.entryThumbnail, requireCurator)
	e.GET("/api/media/entries/:id/playback", h.entryPlayback, requireCurator)
	e.HEAD("/api/media/entries/:id/playback", h.entryPlayback, requireCurator)
	e.GET("/api/media/faces/:sourceID/thumbnail", h.faceThumbnail, requireCurator)
	e.HEAD("/api/media/faces/:sourceID/thumbnail", h.faceThumbnail, requireCurator)
	e.GET("/api/media/people/:id/avatar", h.personAvatar, requirePerson)
	e.HEAD("/api/media/people/:id/avatar", h.personAvatar, requirePerson)
}
