package archives

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestLimiterBoundsPlanBurstsPerSession(t *testing.T) {
	limit := newLimiter()
	limit.planLimit = 2
	limit.planWindow = time.Minute
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	limit.now = func() time.Time { return now }
	first, second := uuid.New(), uuid.New()

	assert.True(t, limit.allowPlan(first))
	assert.True(t, limit.allowPlan(first))
	assert.False(t, limit.allowPlan(first))
	assert.True(t, limit.allowPlan(second), "one Session must not exhaust another Session's burst")

	now = now.Add(time.Minute)
	assert.True(t, limit.allowPlan(first))
	assert.Len(t, limit.plans, 1, "expired Session entries must not accumulate")
}

func TestLimiterBoundsAndReleasesConcurrentStreamsPerSession(t *testing.T) {
	limit := newLimiter()
	limit.streamLimit = 2
	first, second := uuid.New(), uuid.New()

	assert.True(t, limit.acquireStream(first))
	assert.True(t, limit.acquireStream(first))
	assert.False(t, limit.acquireStream(first))
	assert.True(t, limit.acquireStream(second), "one Session must not consume another Session's stream slots")

	limit.releaseStream(first)
	assert.True(t, limit.acquireStream(first))
	limit.releaseStream(first)
	limit.releaseStream(first)
	assert.NotContains(t, limit.activeStreams, first, "idle Session entries must not accumulate")
}
