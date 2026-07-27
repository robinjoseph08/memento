package sessions

import (
	"testing"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestIdentityLimiterUsesActorTargetAndIPWithinSeparateScopes(t *testing.T) {
	limiter := newLimiter(config.SecurityConfig{Secret: "rate-limit-test-secret-at-least-32-bytes", SignInRateWindow: 15 * time.Minute, SignInEmailLimit: 1, SignInIPLimit: 2})
	assert.True(t, limiter.allowIdentity("email-change", "192.0.2.1", "actor-1", "New@example.com"))
	assert.False(t, limiter.allowIdentity("email-change", "192.0.2.2", "actor-1", "other@example.com"), "actor limits apply across IPs and targets")
	assert.False(t, limiter.allowIdentity("email-change", "192.0.2.2", "actor-2", " new@EXAMPLE.com "), "normalized target limits apply across actors")
	assert.True(t, limiter.allowIdentity("email-recovery", "192.0.2.2", "actor-1", "new@example.com"), "identity workflows use separate limiter scopes")
}

func TestSignInLimiterUsesBothNormalizedEmailAndIP(t *testing.T) {
	limiter := newLimiter(config.SecurityConfig{Secret: "rate-limit-test-secret-at-least-32-bytes", SignInRateWindow: 15 * time.Minute, SignInEmailLimit: 1, SignInIPLimit: 2})
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	limiter.now = func() time.Time { return now }
	assert.True(t, limiter.allow("192.0.2.1", "Alex@example.com"))
	assert.False(t, limiter.allow("192.0.2.2", " alex@EXAMPLE.com "), "the normalized identity dimension must apply across IPs")
	assert.True(t, limiter.allow("192.0.2.1", "other@example.com"))
	assert.False(t, limiter.allow("192.0.2.1", "third@example.com"), "the IP dimension must apply across identities")
	now = now.Add(15 * time.Minute)
	assert.True(t, limiter.allow("192.0.2.1", "alex@example.com"))
	assert.Len(t, limiter.entries, 2, "expired keys must be removed instead of accumulating for the process lifetime")
}
