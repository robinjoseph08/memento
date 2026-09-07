// Package errorstack captures creation-site stacks for unexpected errors.
package errorstack

import (
	"context"
	"errors"

	pkgerrors "github.com/pkg/errors"
)

type stackTracer interface {
	StackTrace() pkgerrors.StackTrace
}

type unwrapper interface {
	Unwrap() error
}

type multiUnwrapper interface {
	Unwrap() []error
}

// Capture adds a stack unless the error already has one. Call it where an
// unexpected external error first enters Memento, then use standard %w wrapping
// above that point.
func Capture(err error) error {
	if err == nil {
		return nil
	}

	var tracer stackTracer
	if errors.As(err, &tracer) {
		return err
	}
	return pkgerrors.WithStack(err)
}

// IsContextCancellation reports whether every error branch represents the
// cancellation of ctx.
func IsContextCancellation(ctx context.Context, err error) bool {
	contextErr := ctx.Err()
	return contextErr != nil && onlyMatches(err, contextErr)
}

func onlyMatches(err, target error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(multiUnwrapper); ok {
		branches := joined.Unwrap()
		if len(branches) == 0 {
			return false
		}
		for _, branch := range branches {
			if !onlyMatches(branch, target) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(unwrapper); ok {
		return onlyMatches(wrapped.Unwrap(), target)
	}
	return errors.Is(err, target)
}

// CaptureContext leaves cancellation from ctx stackless and captures other
// errors. Use it for operations whose context controls expected cancellation.
func CaptureContext(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if IsContextCancellation(ctx, err) {
		return err
	}
	return Capture(err)
}
