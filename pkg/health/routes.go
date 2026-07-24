package health

import "github.com/labstack/echo/v4"

const publicSafePolicy = "policy:public_safe"

// RegisterRoutes registers public liveness and readiness routes.
func RegisterRoutes(e *echo.Echo, service *Service) {
	live := e.GET("/api/health/live", service.Live)
	live.Name = publicSafePolicy
	ready := e.GET("/api/health/ready", service.Ready)
	ready.Name = publicSafePolicy
}
