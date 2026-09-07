package server

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	echologger "github.com/robinjoseph08/golib/echo/v5/middleware/logger"
	"github.com/robinjoseph08/golib/echo/v5/middleware/recovery"
	"github.com/robinjoseph08/memento/internal/webapp"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/uptrace/bun"
)

// New constructs the HTTP server and registers the application's routes and
// middleware.
func New(cfg *config.Config, db *bun.DB) (*http.Server, error) {
	frontend, available, err := webapp.Handler()
	if err != nil {
		return nil, fmt.Errorf("load embedded frontend: %w", err)
	}
	if !available {
		frontend = nil
	}
	return newServer(cfg, frontend, dependencies{
		identity: identity.New(db, nil), connection: immich.New(cfg.ImmichURL, cfg.ImmichAPIKey), health: db.PingContext,
	})
}

type dependencies struct {
	identity   identity.UseCases
	connection immich.Diagnostic
	health     func(context.Context) error
}

func newServer(cfg *config.Config, frontend http.Handler, options ...dependencies) (*http.Server, error) {
	var deps dependencies
	if len(options) > 0 {
		deps = options[0]
	}
	e := echo.New()

	requestBinder, err := binder.New()
	if err != nil {
		return nil, fmt.Errorf("create request binder: %w", err)
	}
	e.Binder = requestBinder

	e.Use(echologger.Middleware())
	e.Use(recovery.Middleware())
	e.Use(browserAPI(cfg.PublicURL))

	health := func(c *echo.Context) error {
		healthy := true
		if deps.health != nil {
			ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
			defer cancel()
			healthy = deps.health(ctx) == nil
		}
		status := http.StatusOK
		if !healthy {
			status = http.StatusServiceUnavailable
		}
		return c.JSON(status, map[string]bool{"healthy": healthy})
	}
	e.GET("/health", health)
	e.HEAD("/health", health)
	if deps.identity != nil {
		handlers := identity.RegisterRoutes(e, cfg, deps.identity)
		immich.RegisterRoutes(e, deps.connection, handlers.RequireSetupOrCurator, handlers.RequireCurator)
	}
	apiNotFound := func(_ *echo.Context) error { return echo.ErrNotFound }
	e.Any("/api", apiNotFound)
	e.Any("/api/*", apiNotFound)

	if frontend != nil {
		e.GET("/*", echo.WrapHandler(frontend))
		e.HEAD("/*", echo.WrapHandler(frontend))
	}

	e.HTTPErrorHandler = errcodes.NewHandler().Handle

	return &http.Server{
		Addr:              net.JoinHostPort(cfg.ServerHost, strconv.Itoa(cfg.ServerPort)),
		Handler:           e,
		ReadHeaderTimeout: 3 * time.Second,
	}, nil
}

// browserAPI enforces same-origin JSON mutations using only the configured URL.
func browserAPI(publicURL string) echo.MiddlewareFunc {
	origin := strings.TrimRight(publicURL, "/")
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			req := c.Request()
			if req.URL.Path != "/api" && !strings.HasPrefix(req.URL.Path, "/api/") {
				return next(c)
			}
			c.Response().Header().Set("Cache-Control", "no-store")
			c.Response().Header().Set("X-Content-Type-Options", "nosniff")
			switch req.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				return next(c)
			}
			if req.Header.Get("Origin") != origin {
				return &errcodes.Error{HTTPCode: 403, Code: "invalid_origin", Message: "This request must come from the configured Memento address."}
			}
			contentType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
			if err != nil || contentType != "application/json" {
				return errcodes.UnsupportedMediaType()
			}
			req.Body = http.MaxBytesReader(c.Response(), req.Body, 64*1024)
			return next(c)
		}
	}
}
