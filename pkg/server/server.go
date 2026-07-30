// Package server constructs the Echo HTTP application.
package server

import (
	"fmt"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	goliblogger "github.com/robinjoseph08/golib/echo/v4/middleware/logger"
	golibrecovery "github.com/robinjoseph08/golib/echo/v4/middleware/recovery"
	"github.com/robinjoseph08/memento/pkg/activity"
	"github.com/robinjoseph08/memento/pkg/archives"
	"github.com/robinjoseph08/memento/pkg/audiences"
	"github.com/robinjoseph08/memento/pkg/binder"
	"github.com/robinjoseph08/memento/pkg/comments"
	"github.com/robinjoseph08/memento/pkg/emaildelivery"
	"github.com/robinjoseph08/memento/pkg/engagement"
	"github.com/robinjoseph08/memento/pkg/errcodes"
	"github.com/robinjoseph08/memento/pkg/events"
	"github.com/robinjoseph08/memento/pkg/family"
	"github.com/robinjoseph08/memento/pkg/favorites"
	"github.com/robinjoseph08/memento/pkg/health"
	"github.com/robinjoseph08/memento/pkg/library"
	"github.com/robinjoseph08/memento/pkg/people"
	"github.com/robinjoseph08/memento/pkg/push"
	"github.com/robinjoseph08/memento/pkg/recipients"
	"github.com/robinjoseph08/memento/pkg/recovery"
	"github.com/robinjoseph08/memento/pkg/repairs"
	"github.com/robinjoseph08/memento/pkg/search"
	"github.com/robinjoseph08/memento/pkg/sessions"
	"github.com/robinjoseph08/memento/pkg/setup"
	"github.com/robinjoseph08/memento/pkg/sources"
	"github.com/robinjoseph08/memento/pkg/suggestions"
	"github.com/robinjoseph08/memento/pkg/visibility"
)

// RouteHandlers is the complete production HTTP route manifest. Keeping route
// ownership here makes duplicate and unclassified routes release-gate failures.
type RouteHandlers struct {
	Health      *health.Service
	Email       *emaildelivery.Handler
	Setup       *setup.Handler
	People      *people.Handler
	Family      *family.Handler
	Visibility  *visibility.Handler
	Recipients  *recipients.Handler
	Sources     *sources.Handler
	Events      *events.Handler
	Repairs     *repairs.Handler
	Suggestions *suggestions.Handler
	Audiences   *audiences.Handler
	Sessions    *sessions.Handler
	Library     *library.Handler
	Archives    *archives.Handler
	Search      *search.Handler
	Comments    *comments.Handler
	Favorites   *favorites.Handler
	Activity    *activity.Handler
	Engagement  *engagement.Handler
	Push        *push.Handler
	Recovery    *recovery.Handler

	RecoveryMiddleware echo.MiddlewareFunc
}

// New constructs the HTTP application and registers every production route once.
func New(publicOrigin string, handlers RouteHandlers) (*echo.Echo, error) {
	e := echo.New()
	requestBinder, err := binder.New()
	if err != nil {
		return nil, fmt.Errorf("initialize request binder: %w", err)
	}
	security, err := browserSecurity(publicOrigin)
	if err != nil {
		return nil, err
	}
	e.Binder = requestBinder
	e.HideBanner = true
	e.HidePort = true
	e.Use(goliblogger.Middleware())
	e.Use(golibrecovery.Middleware())
	e.Use(middleware.BodyLimit("10M"))
	e.Use(security)
	if handlers.RecoveryMiddleware != nil {
		e.Use(handlers.RecoveryMiddleware)
	}

	health.RegisterRoutes(e, handlers.Health)
	if handlers.Email != nil {
		emaildelivery.RegisterRoutes(e, handlers.Email)
	}
	if handlers.Setup != nil {
		setup.RegisterRoutes(e, handlers.Setup)
	}
	if handlers.People != nil {
		people.RegisterRoutes(e, handlers.People)
	}
	if handlers.Family != nil {
		family.RegisterRoutes(e, handlers.Family)
	}
	if handlers.Visibility != nil {
		visibility.RegisterRoutes(e, handlers.Visibility)
	}
	if handlers.Recipients != nil {
		recipients.RegisterRoutes(e, handlers.Recipients)
	}
	if handlers.Sources != nil {
		sources.RegisterRoutes(e, handlers.Sources)
	}
	if handlers.Events != nil {
		events.RegisterRoutes(e, handlers.Events)
	}
	if handlers.Repairs != nil {
		repairs.RegisterRoutes(e, handlers.Repairs)
	}
	if handlers.Suggestions != nil {
		suggestions.RegisterRoutes(e, handlers.Suggestions)
	}
	if handlers.Audiences != nil {
		audiences.RegisterRoutes(e, handlers.Audiences)
	}
	if handlers.Sessions != nil {
		sessions.RegisterRoutes(e, handlers.Sessions)
	}
	if handlers.Library != nil {
		library.RegisterRoutes(e, handlers.Library)
	}
	if handlers.Archives != nil {
		archives.RegisterRoutes(e, handlers.Archives)
	}
	if handlers.Search != nil {
		search.RegisterRoutes(e, handlers.Search)
	}
	if handlers.Comments != nil {
		comments.RegisterRoutes(e, handlers.Comments)
	}
	if handlers.Favorites != nil {
		favorites.RegisterRoutes(e, handlers.Favorites)
	}
	if handlers.Activity != nil {
		activity.RegisterRoutes(e, handlers.Activity)
	}
	if handlers.Engagement != nil {
		engagement.RegisterRoutes(e, handlers.Engagement)
	}
	if handlers.Push != nil {
		push.RegisterRoutes(e, handlers.Push)
	}
	if handlers.Recovery != nil {
		recovery.RegisterRoutes(e, handlers.Recovery)
	}
	e.HTTPErrorHandler = errcodes.NewHandler().Handle
	return e, nil
}
