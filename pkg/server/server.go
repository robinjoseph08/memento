// Package server constructs the Echo HTTP application.
package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	goliblogger "github.com/robinjoseph08/golib/echo/v4/middleware/logger"
	golibrecovery "github.com/robinjoseph08/golib/echo/v4/middleware/recovery"
	"github.com/robinjoseph08/memento/pkg/health"
)

// Problem is the stable, non-sensitive API error document.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	RequestID string `json:"request_id,omitempty"`
}

// New constructs routes and middleware. Health routes use the public-safe policy.
func New(service *health.Service) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(goliblogger.Middleware())
	e.Use(golibrecovery.Middleware())
	e.Use(middleware.BodyLimit("10M"))
	e.HTTPErrorHandler = problemHandler

	e.GET("/api/health/live", service.Live)
	e.GET("/api/health/ready", service.Ready)
	return e
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
		RequestID: goliblogger.IDFromEchoContext(c),
	})
}
