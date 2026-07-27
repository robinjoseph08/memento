// Package server constructs the Echo HTTP application.
package server

import (
	"fmt"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	goliblogger "github.com/robinjoseph08/golib/echo/v4/middleware/logger"
	golibrecovery "github.com/robinjoseph08/golib/echo/v4/middleware/recovery"
	"github.com/robinjoseph08/memento/pkg/audiences"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/family"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/recipients"
	"github.com/robinjoseph08/memento/pkg/repairs"
	"github.com/robinjoseph08/memento/pkg/sessions"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/sources"
	"github.com/robinjoseph08/memento/pkg/suggestions"
	"github.com/robinjoseph08/memento/pkg/visibility"
)

// New constructs the HTTP application and delegates route ownership to handler packages.
func New(healthService *health.Service, emailHandler *emaildelivery.Handler, setupHandler *setup.Handler, peopleHandler *people.Handler, familyHandler *family.Handler, visibilityHandler *visibility.Handler, recipientHandler *recipients.Handler, sourceHandler *sources.Handler, eventHandler *events.Handler, repairHandler *repairs.Handler, suggestionHandler *suggestions.Handler, audienceHandler *audiences.Handler, sessionHandlers ...*sessions.Handler) (*echo.Echo, error) {
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
	if visibilityHandler != nil {
		visibility.RegisterRoutes(e, visibilityHandler)
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
	if suggestionHandler != nil {
		suggestions.RegisterRoutes(e, suggestionHandler)
	}
	if audienceHandler != nil {
		audiences.RegisterRoutes(e, audienceHandler)
	}
	if len(sessionHandlers) > 0 && sessionHandlers[0] != nil {
		sessions.RegisterRoutes(e, sessionHandlers[0])
	}
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e, nil
}
