package server

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
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
	"github.com/robinjoseph08/memento/pkg/errorstack"
	"github.com/robinjoseph08/memento/pkg/identity"
	"github.com/robinjoseph08/memento/pkg/immich"
	"github.com/robinjoseph08/memento/pkg/media"
	"github.com/robinjoseph08/memento/pkg/notifications"
	"github.com/robinjoseph08/memento/pkg/publishing"
	"github.com/uptrace/bun"
)

// Features are the modules main wires after constructing the worker runtime.
// Tests pass the zero value to exercise identity routes alone; a nil media
// module serves media through a client of its own.
type Features struct {
	Publishing    *publishing.Module
	Notifications *notifications.Module
	Media         *media.Module
}

// New constructs the HTTP server and registers the application's routes and
// middleware.
func New(cfg *config.Config, db *bun.DB, features Features) (*http.Server, error) {
	frontend, available, err := webapp.Handler(cfg.PublicURL, publicPageMetadata)
	if err != nil {
		return nil, fmt.Errorf("load embedded frontend: %w", err)
	}
	if !available {
		frontend = nil
	}
	source := immich.New(cfg.ImmichURL, cfg.ImmichAPIKey)
	people := identity.New(db, nil)
	people.ImmichURL = cfg.ImmichBrowserURL()
	people.PublicURL = cfg.PublicURL
	if features.Notifications != nil {
		people.Mail = features.Notifications
		people.Announcements = features.Notifications
	}
	library := features.Media
	if library == nil {
		library = media.New(db, source)
	}
	deps := dependencies{identity: people, connection: source, health: db.PingContext, media: library, publishing: features.Publishing}
	if features.Notifications != nil {
		deps.notifications = features.Notifications
	}
	return newServer(cfg, frontend, deps)
}

type dependencies struct {
	identity   identity.UseCases
	connection immich.Diagnostic
	health     func(context.Context) error
	publishing *publishing.Module
	media      *media.Module
	// notifications is nil in tests that exercise identity routes alone.
	notifications notifications.UseCases
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
	e.Use(capturePanicErrorStack())
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
		return errorstack.CaptureContext(c.Request().Context(), c.JSON(status, map[string]bool{"healthy": healthy}))
	}
	e.GET("/health", health)
	e.HEAD("/health", health)
	if deps.identity != nil {
		handlers := identity.RegisterRoutes(e, cfg, deps.identity)
		immich.RegisterRoutes(e, deps.connection, handlers.RequireSetupOrCurator, handlers.RequireCurator)
		if deps.publishing != nil {
			publishing.RegisterRoutes(e, deps.publishing, handlers.RequireCurator)
			publishing.RegisterViewerRoutes(e, deps.publishing, handlers.RequirePerson, handlers.RequireCurator)
			if deps.media != nil {
				media.RegisterViewerRoutes(e, deps.media, deps.publishing.AuthorizeViewerEntry, handlers.RequirePerson, handlers.RequireCurator)
			}
		}
		if deps.media != nil {
			media.RegisterRoutes(e, deps.media, handlers.RequirePerson, handlers.RequireCurator)
		}
		if deps.notifications != nil {
			notifications.RegisterRoutes(e, deps.notifications, handlers.RequirePerson, handlers.RequireCurator)
		}
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

// capturePanicErrorStack preserves the panic site before Golib's recovery
// middleware converts an error panic into a returned error.
func capturePanicErrorStack() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				recoveredErr, ok := recovered.(error)
				if !ok {
					panic(recovered)
				}
				if recoveredErr == http.ErrAbortHandler { //nolint:errorlint // net/http requires the exact sentinel.
					panic(recoveredErr)
				}
				panic(errorstack.Capture(recoveredErr))
			}()
			return next(c)
		}
	}
}

// browserAPI enforces same-origin JSON mutations. A mutation must come from
// the configured URL, or from the address the browser used to reach this
// server, which lets a development machine answer by hostname over plain HTTP.
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
			if !sameOrigin(req, origin) {
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

func sameOrigin(req *http.Request, publicOrigin string) bool {
	origin := req.Header.Get("Origin")
	if origin == publicOrigin {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && parsed.Host == req.Host
}
