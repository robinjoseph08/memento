// Package lifecycle coordinates bounded graceful shutdown.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ReadinessGate drops readiness before any draining begins.
type ReadinessGate interface {
	SetDraining()
}

// HTTPServer stops new requests and drains accepted requests.
type HTTPServer interface {
	Shutdown(ctx context.Context) error
}

// Worker stops claims and releases leases after bounded work drains.
type Worker interface {
	StopClaims()
	Drain(ctx context.Context) error
}

// Closer closes the PostgreSQL pool last.
type Closer interface {
	Close() error
}

// Shutdown performs the required sequence within the overall deadline and worker drain bound.
func Shutdown(ctx context.Context, workerDrainTimeout time.Duration, gate ReadinessGate, server HTTPServer, worker Worker, database Closer) error {
	gate.SetDraining()
	worker.StopClaims()

	var shutdownErrors []error
	if err := server.Shutdown(ctx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("drain HTTP: %w", err))
	}
	workerCtx, cancelWorker := context.WithTimeout(ctx, workerDrainTimeout)
	if err := worker.Drain(workerCtx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("drain worker: %w", err))
	}
	cancelWorker()
	if err := database.Close(); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("close database: %w", err))
	}
	return errors.Join(shutdownErrors...)
}
