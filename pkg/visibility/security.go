package visibility

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	directorySearchRateWindow = time.Minute
	directorySearchRateLimit  = 30
)

type directoryRateEntry struct {
	started time.Time
	count   int
}

type directorySearchLimiter struct {
	mu          sync.Mutex
	window      time.Duration
	limit       int
	entries     map[string]directoryRateEntry
	now         func() time.Time
	lastCleanup time.Time
}

func newDirectorySearchLimiter() *directorySearchLimiter {
	return &directorySearchLimiter{
		window:  directorySearchRateWindow,
		limit:   directorySearchRateLimit,
		entries: make(map[string]directoryRateEntry),
		now:     time.Now,
	}
}

func (l *directorySearchLimiter) allow(accessID uuid.UUID, clientIP string) bool {
	keys := []string{"access:" + accessID.String()}
	if clientIP != "" {
		keys = append(keys, "ip:"+clientIP)
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
		entry := l.entries[key]
		if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
			entry = directoryRateEntry{started: now}
		}
		if entry.count >= l.limit {
			return false
		}
	}
	for _, key := range keys {
		entry := l.entries[key]
		if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
			entry = directoryRateEntry{started: now}
		}
		entry.count++
		l.entries[key] = entry
	}
	return true
}
