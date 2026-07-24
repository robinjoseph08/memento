package setup

import (
	"context"
	"crypto/tls"
	"net/http/httptest"
	"net/netip"
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

func TestSetupCompletionRequiresHTTPSOrLoopback(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	insecure := httptest.NewRequestWithContext(context.Background(), "POST", "http://memento.example/api/setup/complete", nil)
	insecure.RemoteAddr = "203.0.113.7:1234"
	assert.False(t, secureSetupCompletion(insecure, trusted))

	secure := insecure.Clone(context.Background())
	secure.TLS = new(tls.ConnectionState)
	assert.True(t, secureSetupCompletion(secure, trusted))

	proxied := insecure.Clone(context.Background())
	proxied.RemoteAddr = "127.0.0.1:1234"
	proxied.Header.Set("X-Forwarded-Proto", "https")
	assert.True(t, secureSetupCompletion(proxied, trusted))
	assert.False(t, secureSetupCompletion(proxied, nil))

	loopback := httptest.NewRequestWithContext(context.Background(), "POST", "http://localhost/api/setup/complete", nil)
	assert.True(t, secureSetupCompletion(loopback, nil))
}

func TestClientIPTrustsForwardingOnlyFromLoopback(t *testing.T) {
	proxied := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
	proxied.RemoteAddr = "127.0.0.1:1234"
	proxied.Header.Set("X-Forwarded-For", "198.51.100.10, 192.0.2.5")
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	assert.Equal(t, "192.0.2.5", clientIP(proxied, trusted))
	proxied.Header.Set("X-Memento-Client-IP", "198.51.100.20")
	assert.Equal(t, "198.51.100.20", clientIP(proxied, trusted))
	assert.Equal(t, "127.0.0.1", clientIP(proxied, nil))

	direct := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
	direct.RemoteAddr = "203.0.113.7:1234"
	direct.Header.Set("X-Forwarded-For", "198.51.100.10")
	assert.Equal(t, "203.0.113.7", clientIP(direct, trusted))
}
