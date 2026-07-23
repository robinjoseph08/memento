// Package health provides allowlisted liveness and readiness responses.
package health

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/robinjoseph08/memento/pkg/migrations"
	"github.com/uptrace/bun"
)

const (
	StatusOK          = "ok"
	StatusUnavailable = "unavailable"
)

// LiveResponse is generated to TypeScript by Tygo.
type LiveResponse struct {
	Status string `json:"status"`
}

// ReadyChecks contains only allowlisted dependency states.
type ReadyChecks struct {
	PostgreSQL string `json:"postgresql"`
	Migrations string `json:"migrations"`
	Setup      string `json:"setup"`
	Worker     string `json:"worker"`
	Immich     string `json:"immich"`
}

// ReadyResponse is generated to TypeScript by Tygo.
type ReadyResponse struct {
	Status string      `json:"status"`
	Checks ReadyChecks `json:"checks"`
}

// Checker is a safe dependency readiness check.
type Checker interface {
	Check(ctx context.Context) error
}

// Worker exposes freshness without a database call.
type Worker interface {
	Healthy(maxAge time.Duration) bool
}

// Service owns process state and readiness dependencies.
type Service struct {
	postgres        func(context.Context) error
	migration       func(context.Context) error
	setup           func(context.Context) error
	immich          Checker
	worker          Worker
	databaseTimeout time.Duration
	heartbeatMaxAge time.Duration
	draining        atomic.Bool
}

func New(db *bun.DB, immich Checker, worker Worker, databaseTimeout, heartbeatMaxAge time.Duration) *Service {
	return newWithChecks(
		db.PingContext,
		func(ctx context.Context) error { return migrations.Current(ctx, db) },
		func(ctx context.Context) error { return migrations.SetupConsistent(ctx, db) },
		immich,
		worker,
		databaseTimeout,
		heartbeatMaxAge,
	)
}

func newWithChecks(postgres, migration, setup func(context.Context) error, immich Checker, worker Worker, databaseTimeout, heartbeatMaxAge time.Duration) *Service {
	return &Service{postgres: postgres, migration: migration, setup: setup, immich: immich, worker: worker, databaseTimeout: databaseTimeout, heartbeatMaxAge: heartbeatMaxAge}
}

// SetDraining drops readiness synchronously before shutdown work starts.
func (s *Service) SetDraining() {
	s.draining.Store(true)
}

// IsDraining reports the process gate without dependency calls.
func (s *Service) IsDraining() bool {
	return s.draining.Load()
}

// Live reports process liveness and intentionally performs no dependency calls.
func (s *Service) Live(c echo.Context) error {
	return c.JSON(http.StatusOK, LiveResponse{Status: "live"})
}

// Ready checks each required dependency with secret-safe status values.
func (s *Service) Ready(c echo.Context) error {
	checks := ReadyChecks{
		PostgreSQL: StatusUnavailable,
		Migrations: StatusUnavailable,
		Setup:      StatusUnavailable,
		Worker:     StatusUnavailable,
		Immich:     StatusUnavailable,
	}
	if s.draining.Load() {
		return c.JSON(http.StatusServiceUnavailable, ReadyResponse{Status: "not_ready", Checks: checks})
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), s.databaseTimeout)
	defer cancel()
	if err := s.postgres(ctx); err == nil {
		checks.PostgreSQL = StatusOK
		if err := s.migration(ctx); err == nil {
			checks.Migrations = StatusOK
		}
		if err := s.setup(ctx); err == nil {
			checks.Setup = StatusOK
		}
	}
	if s.worker.Healthy(s.heartbeatMaxAge) {
		checks.Worker = StatusOK
	}
	if err := s.immich.Check(c.Request().Context()); err == nil {
		checks.Immich = StatusOK
	}
	status := http.StatusOK
	label := "ready"
	if checks.PostgreSQL != StatusOK || checks.Migrations != StatusOK || checks.Setup != StatusOK || checks.Worker != StatusOK || checks.Immich != StatusOK {
		status = http.StatusServiceUnavailable
		label = "not_ready"
	}
	return c.JSON(status, ReadyResponse{Status: label, Checks: checks})
}
