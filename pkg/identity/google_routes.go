package identity

import (
	"github.com/labstack/echo/v5"
	"github.com/robinjoseph08/memento/pkg/config"
)

// RegisterGoogleRoutes adds browser sign-in only in Google mode, without network I/O.
// Options substitute the provider for local protocol and HTTP tests.
func RegisterGoogleRoutes(e *echo.Echo, cfg *config.Config, module UseCases, options ...GoogleOption) {
	if cfg.AuthMode != "google" {
		return
	}
	h := &googleHandlers{
		identity:     &Handlers{module: module, publicURL: cfg.PublicURL, authMode: cfg.AuthMode},
		provider:     NewGoogleProvider(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleCallbackURL, options...),
		transactions: make(map[string]googleTransaction),
	}
	e.GET("/api/identity/google/start", h.start)
	e.GET("/api/identity/google/callback", h.callback)
}
