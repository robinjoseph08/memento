package recovery

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/errcodes"
)

var recoveryAllowlist = map[string]map[string]bool{
	"/api/health/live":              {http.MethodGet: true},
	"/api/health/ready":             {http.MethodGet: true},
	"/api/recovery/status":          {http.MethodGet: true},
	"/api/recovery/review":          {http.MethodGet: true},
	"/api/recovery/review/complete": {http.MethodPost: true},
	"/api/recovery/release":         {http.MethodPost: true},
	"/api/auth/sign-in/request":     {http.MethodPost: true},
	"/api/auth/sign-in/verify":      {http.MethodPost: true},
	"/api/session":                  {http.MethodGet: true},
	"/api/session/logout":           {http.MethodPost: true},
}

// Middleware blocks every non-liveness API outside the fresh Curator recovery workflow.
func (s *Service) Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path
			if !strings.HasPrefix(path, "/api/") || path == "/api/health/live" {
				return next(c)
			}
			// Readiness acquires this same traffic fence after PostgreSQL has
			// proved reachable, so a database outage can still return its safe,
			// allowlisted readiness payload instead of a generic server error.
			if path == "/api/health/ready" {
				return next(c)
			}
			releaseFence, err := s.Acquire(c.Request().Context())
			if err != nil {
				return err
			}
			defer releaseFence()
			if recoveryAllowlist[path][c.Request().Method] {
				return next(c)
			}
			held, err := s.Held(c.Request().Context())
			if err != nil {
				return err
			}
			if held {
				return errcodes.ServiceUnavailable("Memento is awaiting Curator recovery review.")
			}
			return next(c)
		}
	}
}
