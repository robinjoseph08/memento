package server

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/robinjoseph08/golib/echo/v5/health"
	echologger "github.com/robinjoseph08/golib/echo/v5/middleware/logger"
	"github.com/robinjoseph08/golib/echo/v5/middleware/recovery"
	"github.com/robinjoseph08/web-app-template/internal/webapp"
	"github.com/robinjoseph08/web-app-template/pkg/binder"
	"github.com/robinjoseph08/web-app-template/pkg/config"
	"github.com/robinjoseph08/web-app-template/pkg/errcodes"
)

// New constructs the HTTP server and registers the application's routes and
// middleware.
func New(cfg *config.Config) (*http.Server, error) {
	frontend, available, err := webapp.Handler()
	if err != nil {
		return nil, fmt.Errorf("load embedded frontend: %w", err)
	}
	if !available {
		frontend = nil
	}
	return newServer(cfg, frontend)
}

func newServer(cfg *config.Config, frontend http.Handler) (*http.Server, error) {
	e := echo.New()

	requestBinder, err := binder.New()
	if err != nil {
		return nil, fmt.Errorf("create request binder: %w", err)
	}
	e.Binder = requestBinder

	e.Use(echologger.Middleware())
	e.Use(recovery.Middleware())
	e.Use(middleware.CORS("*"))

	health.RegisterRoutes(e)
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
