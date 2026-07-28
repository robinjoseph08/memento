package search

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestSearchLimiterBoundsEachAccessGenerationAndExpiresWindows(t *testing.T) {
	limiter := newSearchLimiter()
	limiter.limit = 2
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	first := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	second := uuid.MustParse("10000000-0000-0000-0000-000000000002")

	assert.True(t, limiter.allow(first))
	assert.True(t, limiter.allow(first))
	assert.False(t, limiter.allow(first))
	assert.True(t, limiter.allow(second))

	now = now.Add(searchRateWindow)
	assert.True(t, limiter.allow(first))
	assert.Len(t, limiter.entries, 1, "expired access generations must not accumulate")
}
