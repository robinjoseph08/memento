package errorstack

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureAtKnownOrigin(err error) error {
	return Capture(err)
}

func TestCaptureRecordsOriginAndPreservesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("database failed")
	captured := captureAtKnownOrigin(cause)

	require.ErrorIs(t, captured, cause)
	var tracer stackTracer
	require.ErrorAs(t, captured, &tracer)
	stack := fmt.Sprintf("%+v", tracer.StackTrace())
	assert.Contains(t, stack, "errorstack.captureAtKnownOrigin")
	assert.Contains(t, stack, "errorstack_test.go")
}

func TestCaptureDoesNotReplaceExistingStack(t *testing.T) {
	t.Parallel()

	captured := captureAtKnownOrigin(errors.New("database failed"))
	assert.Same(t, captured, Capture(captured))
}

func TestCaptureContextLeavesCancellationStackless(t *testing.T) {
	t.Parallel()

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, cancelDeadline := context.WithDeadline(context.Background(), time.Time{})
	t.Cleanup(cancelDeadline)

	for _, ctx := range []context.Context{canceled, deadline} {
		err := errors.Join(ctx.Err(), fmt.Errorf("stop operation: %w", ctx.Err()))
		var tracer stackTracer
		assert.True(t, IsContextCancellation(ctx, err))
		assert.NotErrorAs(t, CaptureContext(ctx, err), &tracer)
	}
}

func TestCaptureContextCapturesMixedCancellationAndFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failure := errors.New("close connection")
	err := fmt.Errorf("stop operation: %w", errors.Join(ctx.Err(), failure))

	assert.False(t, IsContextCancellation(ctx, err))
	require.ErrorIs(t, CaptureContext(ctx, err), failure)
	var tracer stackTracer
	require.ErrorAs(t, CaptureContext(ctx, err), &tracer)
}

func TestActiveContextDoesNotClassifyNestedCancellation(t *testing.T) {
	t.Parallel()

	assert.False(t, IsContextCancellation(context.Background(), context.Canceled))
}

func TestCaptureRecordsDeadlineOutsideCanceledOperation(t *testing.T) {
	t.Parallel()

	var tracer stackTracer
	require.ErrorAs(t, Capture(context.DeadlineExceeded), &tracer)
}
