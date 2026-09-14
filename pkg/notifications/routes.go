package notifications

import "github.com/labstack/echo/v5"

// RegisterRoutes keeps previews, approval, and delivery state behind the
// Curator guard and a Person's own notifications behind the signed-in guard.
// The unsubscribe link is public by design: it is opened from an email
// without a session, and only its POST changes anything. Its token is a
// query value so the access log, which records paths, never carries it.
func RegisterRoutes(e *echo.Echo, module UseCases, requirePerson, requireCurator echo.MiddlewareFunc) {
	h := &handlers{module: module}
	e.POST("/api/curator/notifications/preview", h.preview, requireCurator)
	e.POST("/api/curator/notifications/approve", h.approve, requireCurator)
	e.GET("/api/curator/notifications/deliveries", h.deliveries, requireCurator)
	e.POST("/api/curator/notifications/deliveries/:id/retry", h.retryDelivery, requireCurator)
	e.GET("/api/notifications", h.list, requirePerson)
	e.POST("/api/notifications/read-all", h.markAllRead, requirePerson)
	e.POST("/api/notifications/:id/read", h.markRead, requirePerson)
	e.GET("/api/unsubscribe", h.unsubscribeStatus)
	e.POST("/api/unsubscribe", h.unsubscribe)
}
