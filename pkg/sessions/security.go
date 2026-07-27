package sessions

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/robinjoseph08/memento/pkg/config"
)

type rateEntry struct {
	started time.Time
	count   int
}
type limiter struct {
	mu                  sync.Mutex
	window              time.Duration
	emailLimit, ipLimit int
	secret              []byte
	entries             map[string]rateEntry
	now                 func() time.Time
}

func newLimiter(cfg config.SecurityConfig) *limiter {
	return &limiter{window: cfg.SignInRateWindow, emailLimit: cfg.SignInEmailLimit, ipLimit: cfg.SignInIPLimit, secret: []byte(cfg.Secret), entries: make(map[string]rateEntry), now: time.Now}
}

func (l *limiter) allow(ip, email string) bool {
	keys := make([]struct {
		key   string
		limit int
	}, 0, 2)
	if ip != "" {
		keys = append(keys, struct {
			key   string
			limit int
		}{"ip:" + ip, l.ipLimit})
	}
	if email != "" {
		mac := hmac.New(sha256.New, l.secret)
		_, _ = mac.Write([]byte("sign-in-rate:" + strings.ToLower(strings.TrimSpace(email))))
		keys = append(keys, struct {
			key   string
			limit int
		}{"email:" + hex.EncodeToString(mac.Sum(nil)), l.emailLimit})
	}
	if len(keys) == 0 {
		return true
	}
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, value := range keys {
		entry := l.entries[value.key]
		if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
			entry = rateEntry{started: now}
		}
		if entry.count >= value.limit {
			return false
		}
	}
	for _, value := range keys {
		entry := l.entries[value.key]
		if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
			entry = rateEntry{started: now}
		}
		entry.count++
		l.entries[value.key] = entry
	}
	return true
}
