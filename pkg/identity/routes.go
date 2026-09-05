package identity

import (
	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/config"
)

// RegisterRoutes registers public authentication routes. Protected features reuse the guards.
func RegisterRoutes(e *echo.Echo, cfg *config.Config, module UseCases) *Handlers {
	h := &Handlers{module: module, publicURL: cfg.PublicURL, authMode: cfg.AuthMode}
	e.GET("/api/identity/status", h.status)
	e.GET("/api/identity/me", h.me, h.RequireCurator)
	e.POST("/api/identity/sign-out", h.signOut)
	if cfg.AuthMode == "fake" && (cfg.AppEnv == "development" || cfg.AppEnv == "test") {
		e.POST("/api/identity/fake-sign-in", h.fakeSignIn)
	}
	return h
}
