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
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/family"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/recipients"
	"github.com/robinjoseph08/memento/pkg/repairs"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/sources"
)

// New constructs the HTTP application and delegates route ownership to handler packages.
func New(healthService *health.Service, emailHandler *emaildelivery.Handler, setupHandler *setup.Handler, peopleHandler *people.Handler, familyHandler *family.Handler, recipientHandler *recipients.Handler, sourceHandler *sources.Handler, eventHandler *events.Handler, repairHandler *repairs.Handler) (*echo.Echo, error) {
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
	if peopleHandler != nil {
		people.RegisterRoutes(e, peopleHandler)
	}
	if familyHandler != nil {
		family.RegisterRoutes(e, familyHandler)
	}
	if recipientHandler != nil {
		recipients.RegisterRoutes(e, recipientHandler)
	}
	if sourceHandler != nil {
		sources.RegisterRoutes(e, sourceHandler)
	}
	if eventHandler != nil {
		events.RegisterRoutes(e, eventHandler)
	}
	if repairHandler != nil {
		repairs.RegisterRoutes(e, repairHandler)
	}
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e, nil
}
