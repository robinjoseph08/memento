package recipients

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
)

type acceptanceWindow struct {
	startedAt time.Time
	count     int
}

type acceptanceLimiter struct {
	mu          sync.Mutex
	window      time.Duration
	limit       int
	secret      []byte
	entries     map[string]acceptanceWindow
	now         func() time.Time
	lastCleanup time.Time
}

func newAcceptanceLimiter(cfg config.SecurityConfig) *acceptanceLimiter {
	return &acceptanceLimiter{
		window:  cfg.InvitationAcceptRateWindow,
		limit:   cfg.InvitationAcceptIPLimit,
		secret:  []byte(cfg.Secret),
		entries: make(map[string]acceptanceWindow),
		now:     time.Now,
	}
}

func (l *acceptanceLimiter) allow(clientIP, token string) bool {
	if l == nil || l.window <= 0 || l.limit <= 0 {
		return true
	}
	mac := hmac.New(sha256.New, l.secret)
	_, _ = mac.Write([]byte("invitation-accept-rate:"))
	_, _ = mac.Write([]byte(token))
	keys := []string{"token:" + hex.EncodeToString(mac.Sum(nil))}
	if clientIP != "" {
		keys = append(keys, "ip:"+clientIP)
	}

	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastCleanup.IsZero() || now.Sub(l.lastCleanup) >= l.window {
		for key, entry := range l.entries {
			if now.Sub(entry.startedAt) >= l.window {
				delete(l.entries, key)
			}
		}
		l.lastCleanup = now
	}
	for _, key := range keys {
		entry := l.entries[key]
		if now.Sub(entry.startedAt) < l.window && entry.count >= l.limit {
			return false
		}
	}
	for _, key := range keys {
		entry := l.entries[key]
		if now.Sub(entry.startedAt) >= l.window {
			entry = acceptanceWindow{startedAt: now}
		}
		entry.count++
		l.entries[key] = entry
	}
	return true
}
