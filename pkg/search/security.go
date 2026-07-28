package search

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	searchRateWindow = time.Minute
	searchRateLimit  = 30
)

type searchRateEntry struct {
	started time.Time
	count   int
}

type searchLimiter struct {
	mu          sync.Mutex
	window      time.Duration
	limit       int
	entries     map[uuid.UUID]searchRateEntry
	now         func() time.Time
	lastCleanup time.Time
}

func newSearchLimiter() *searchLimiter {
	return &searchLimiter{
		window:  searchRateWindow,
		limit:   searchRateLimit,
		entries: make(map[uuid.UUID]searchRateEntry),
		now:     time.Now,
	}
}

func (l *searchLimiter) allow(accessID uuid.UUID) bool {
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
	entry := l.entries[accessID]
	if entry.started.IsZero() || now.Sub(entry.started) >= l.window {
		entry = searchRateEntry{started: now}
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.entries[accessID] = entry
	return true
}
