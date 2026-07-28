package archives

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	archivePlanRateWindow     = 15 * time.Minute
	archivePlanSessionLimit   = 3
	archiveStreamSessionLimit = 2
)

type planRateEntry struct {
	startedAt time.Time
	count     int
}

type limiter struct {
	mu              sync.Mutex
	planWindow      time.Duration
	planLimit       int
	streamLimit     int
	plans           map[uuid.UUID]planRateEntry
	activeStreams   map[uuid.UUID]int
	now             func() time.Time
	lastPlanCleanup time.Time
}

func newLimiter() *limiter {
	return &limiter{
		planWindow:    archivePlanRateWindow,
		planLimit:     archivePlanSessionLimit,
		streamLimit:   archiveStreamSessionLimit,
		plans:         make(map[uuid.UUID]planRateEntry),
		activeStreams: make(map[uuid.UUID]int),
		now:           time.Now,
	}
}

func (l *limiter) allowPlan(sessionID uuid.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now().UTC()
	if l.lastPlanCleanup.IsZero() || !now.Before(l.lastPlanCleanup.Add(l.planWindow)) {
		for id, entry := range l.plans {
			if !now.Before(entry.startedAt.Add(l.planWindow)) {
				delete(l.plans, id)
			}
		}
		l.lastPlanCleanup = now
	}
	entry, exists := l.plans[sessionID]
	if !exists || !now.Before(entry.startedAt.Add(l.planWindow)) {
		l.plans[sessionID] = planRateEntry{startedAt: now, count: 1}
		return true
	}
	if entry.count >= l.planLimit {
		return false
	}
	entry.count++
	l.plans[sessionID] = entry
	return true
}

func (l *limiter) acquireStream(sessionID uuid.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.activeStreams[sessionID] >= l.streamLimit {
		return false
	}
	l.activeStreams[sessionID]++
	return true
}

func (l *limiter) releaseStream(sessionID uuid.UUID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.activeStreams[sessionID] <= 1 {
		delete(l.activeStreams, sessionID)
		return
	}
	l.activeStreams[sessionID]--
}
