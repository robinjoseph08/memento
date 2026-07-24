// Package server constructs the Echo HTTP application.
package server

import (
	"fmt"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	goliblogger "github.com/robinjoseph08/golib/echo/v4/middleware/logger"
	golibrecovery "github.com/robinjoseph08/golib/echo/v4/middleware/recovery"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/setup"
)

// New constructs the HTTP application and delegates route ownership to handler packages.
func New(healthService *health.Service, emailHandler *emaildelivery.Handler, setupHandler *setup.Handler, peopleHandlers ...*people.Handler) (*echo.Echo, error) {
	e := echo.New()
	requestBinder, err := binder.New()
	if err != nil {
		return nil, fmt.Errorf("initialize request binder: %w", err)
	}
	e.Binder = requestBinder
	e.HideBanner = true
	e.HidePort = true
	e.Use(goliblogger.Middleware())
	e.Use(golibrecovery.Middleware())
	e.Use(middleware.BodyLimit("10M"))

	health.RegisterRoutes(e, healthService)
	if emailHandler != nil {
		emaildelivery.RegisterRoutes(e, emailHandler)
	}
	if setupHandler != nil {
		setup.RegisterRoutes(e, setupHandler)
	}
	if len(peopleHandlers) > 0 && peopleHandlers[0] != nil {
		people.RegisterRoutes(e, peopleHandlers[0])
	}
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e, nil
}
