package setup

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestSetupLimiterBoundsIPAndNormalizedEmail(t *testing.T) {
	limiter := newSetupLimiter(config.SecurityConfig{
		Secret: "test-only-security-secret-32-bytes", SetupRateWindow: time.Minute,
		SetupEmailLimit: 2, SetupIPLimit: 3,
	})
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }

	assert.True(t, limiter.allowRequestCode("192.0.2.1", "Robin@Example.com"))
	assert.True(t, limiter.allowRequestCode("192.0.2.2", " robin@example.com "))
	assert.False(t, limiter.allowRequestCode("192.0.2.3", "ROBIN@example.com"))
	assert.True(t, limiter.allowIP("192.0.2.1"))
	assert.True(t, limiter.allowIP("192.0.2.1"))
	assert.False(t, limiter.allowIP("192.0.2.1"))

	now = now.Add(time.Minute)
	assert.True(t, limiter.allowRequestCode("192.0.2.1", "robin@example.com"))
}

func TestClientIPTrustsForwardingOnlyFromLoopback(t *testing.T) {
	proxied := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
	proxied.RemoteAddr = "127.0.0.1:1234"
	proxied.Header.Set("X-Forwarded-For", "198.51.100.10, 192.0.2.5")
	assert.Equal(t, "192.0.2.5", clientIP(proxied))

	direct := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
	direct.RemoteAddr = "203.0.113.7:1234"
	direct.Header.Set("X-Forwarded-For", "198.51.100.10")
	assert.Equal(t, "203.0.113.7", clientIP(direct))
}
