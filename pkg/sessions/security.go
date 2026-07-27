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

type rateKey struct {
	value string
	limit int
}

type limiter struct {
	mu                  sync.Mutex
	window              time.Duration
	emailLimit, ipLimit int
	secret              []byte
	entries             map[string]rateEntry
	now                 func() time.Time
	lastCleanup         time.Time
}

func newLimiter(cfg config.SecurityConfig) *limiter {
	return &limiter{window: cfg.SignInRateWindow, emailLimit: cfg.SignInEmailLimit, ipLimit: cfg.SignInIPLimit, secret: []byte(cfg.Secret), entries: make(map[string]rateEntry), now: time.Now}
}

func (l *limiter) allow(ip, email string) bool {
	keys := make([]rateKey, 0, 2)
	if ip != "" {
		keys = append(keys, rateKey{value: "sign-in:ip:" + ip, limit: l.ipLimit})
	}
	if email != "" {
		keys = append(keys, rateKey{value: l.privateKey("sign-in:email", email), limit: l.emailLimit})
	}
	return l.allowKeys(keys)
}

func (l *limiter) allowIdentity(scope, ip, actor, target string) bool {
	keys := make([]rateKey, 0, 3)
	if ip != "" {
		keys = append(keys, rateKey{value: scope + ":ip:" + ip, limit: l.ipLimit})
	}
	if actor != "" {
		keys = append(keys, rateKey{value: l.privateKey(scope+":actor", actor), limit: l.emailLimit})
	}
	if target != "" {
		keys = append(keys, rateKey{value: l.privateKey(scope+":target", target), limit: l.emailLimit})
	}
	return l.allowKeys(keys)
}

func (l *limiter) privateKey(scope, value string) string {
	mac := hmac.New(sha256.New, l.secret)
	_, _ = mac.Write([]byte(scope + ":" + strings.ToLower(strings.TrimSpace(value))))
	return scope + ":" + hex.EncodeToString(mac.Sum(nil))
}

func (l *limiter) allowKeys(keys []rateKey) bool {
	if len(keys) == 0 {
		return true
	}
	now := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastCleanup.IsZero() || now.Sub(l.lastCleanup) >= l.window {
		for key, entry := range l.entries {
			if now.Sub(entry.started) >= l.window {
				delete(l.entries, key)
			}
		}
		l.lastCleanup = now
	}
	for _, key := range keys {
		entry := l.entries[key.value]
		if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
			entry = rateEntry{started: now}
		}
		if entry.count >= key.limit {
			return false
		}
	}
	for _, key := range keys {
		entry := l.entries[key.value]
		if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
			entry = rateEntry{started: now}
		}
		entry.count++
		l.entries[key.value] = entry
	}
	return true
}
