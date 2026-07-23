// Package server constructs the Echo HTTP application.
package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	goliblogger "github.com/robinjoseph08/golib/echo/v4/middleware/logger"
	golibrecovery "github.com/robinjoseph08/golib/echo/v4/middleware/recovery"
	"github.com/robinjoseph08/memento/pkg/health"
)

// Problem is the stable, non-sensitive API error document.
type Problem struct {
	Type        string            `json:"type"`
	Title       string            `json:"title"`
	Status      int               `json:"status"`
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	FieldErrors map[string]string `json:"field_errors,omitempty"`
	RequestID   string            `json:"request_id,omitempty"`
}

type routePolicy string

const publicSafe routePolicy = "public_safe"

// New constructs routes and middleware. Health routes use the public-safe policy.
func New(service *health.Service) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(goliblogger.Middleware())
	e.Use(golibrecovery.Middleware())
	e.Use(middleware.BodyLimit("10M"))
	e.HTTPErrorHandler = problemHandler

	registerRoute(e, http.MethodGet, "/api/health/live", service.Live, publicSafe)
	registerRoute(e, http.MethodGet, "/api/health/ready", service.Ready, publicSafe)
	return e
}

func registerRoute(e *echo.Echo, method, path string, handler echo.HandlerFunc, policy routePolicy) *echo.Route {
	if policy == "" {
		panic("route policy is required")
	}
	route := e.Add(method, path, handler)
	route.Name = "policy:" + string(policy)
	return route
}

func problemHandler(err error, c echo.Context) {
	if c.Response().Committed {
		return
	}
	status := http.StatusInternalServerError
	var httpError *echo.HTTPError
	if errors.As(err, &httpError) {
		status = httpError.Code
	}
	title := http.StatusText(status)
	if title == "" {
		title = http.StatusText(http.StatusInternalServerError)
		status = http.StatusInternalServerError
	}
	_ = c.JSON(status, Problem{
		Type:      "about:blank",
		Title:     title,
		Status:    status,
		Code:      "http_" + strconv.Itoa(status),
		Message:   title,
		RequestID: goliblogger.IDFromEchoContext(c),
	})
}
