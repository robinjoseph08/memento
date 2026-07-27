package recipients

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestAcceptanceLimiterBoundsTokenAndClientIP(t *testing.T) {
	limiter := newAcceptanceLimiter(config.SecurityConfig{
		Secret:                     "test-only-security-secret-32-bytes",
		InvitationAcceptRateWindow: time.Minute,
		InvitationAcceptIPLimit:    2,
	})
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	assert.True(t, limiter.allow("192.0.2.1", "first-token"))
	assert.True(t, limiter.allow("192.0.2.2", "first-token"))
	assert.False(t, limiter.allow("192.0.2.3", "first-token"), "one token must have a bounded burst across client IPs")
	assert.True(t, limiter.allow("192.0.2.1", "second-token"))
	assert.False(t, limiter.allow("192.0.2.1", "third-token"), "one client IP must have a bounded burst")

	now = now.Add(time.Minute)
	assert.True(t, limiter.allow("192.0.2.1", "first-token"))
}
