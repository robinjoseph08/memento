// Package lifecycle coordinates bounded graceful shutdown.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
)

// ReadinessGate drops readiness before any draining begins.
type ReadinessGate interface {
	SetDraining()
}

// HTTPServer stops new requests and drains accepted requests.
type HTTPServer interface {
	Shutdown(context.Context) error
}

// Worker stops claims and releases leases after bounded work drains.
type Worker interface {
	StopClaims()
	Drain(context.Context) error
}

// Closer closes the PostgreSQL pool last.
type Closer interface {
	Close() error
}

// Shutdown performs the required shutdown sequence using one caller-supplied deadline.
func Shutdown(ctx context.Context, gate ReadinessGate, server HTTPServer, worker Worker, database Closer) error {
	gate.SetDraining()
	worker.StopClaims()

	var shutdownErrors []error
	if err := server.Shutdown(ctx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("drain HTTP: %w", err))
	}
	if err := worker.Drain(ctx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("drain worker: %w", err))
	}
	if err := database.Close(); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("close database: %w", err))
	}
	return errors.Join(shutdownErrors...)
}
