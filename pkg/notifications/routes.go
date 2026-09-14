package notifications

import "github.com/labstack/echo/v5"

// RegisterRoutes keeps previews and approval behind the Curator guard and a
// Person's own notifications behind the signed-in guard.
func RegisterRoutes(e *echo.Echo, module UseCases, requirePerson, requireCurator echo.MiddlewareFunc) {
	h := &handlers{module: module}
	e.POST("/api/curator/notifications/preview", h.preview, requireCurator)
	e.POST("/api/curator/notifications/approve", h.approve, requireCurator)
	e.GET("/api/notifications", h.list, requirePerson)
	e.POST("/api/notifications/read-all", h.markAllRead, requirePerson)
	e.POST("/api/notifications/:id/read", h.markRead, requirePerson)
}
