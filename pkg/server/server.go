// Package server constructs the Echo HTTP application.
package server

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	goliblogger "github.com/robinjoseph08/golib/echo/v4/middleware/logger"
	golibrecovery "github.com/robinjoseph08/golib/echo/v4/middleware/recovery"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/health"
)

// New constructs the HTTP application and delegates route ownership to handler packages.
func New(healthService *health.Service) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(goliblogger.Middleware())
	e.Use(golibrecovery.Middleware())
	e.Use(middleware.BodyLimit("10M"))

	health.RegisterRoutes(e, healthService)
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e
}
