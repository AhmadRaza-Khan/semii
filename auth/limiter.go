package auth

import (
	"sync"
	"time"
)

// Limiter is an in-memory sliding-window rate limiter, suitable for a
// single instance (v1). It is safe for concurrent use.
type Limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
	now    func() time.Time
}

// NewLimiter allows limit events per key within window.
func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, hits: map[string][]time.Time{}, now: time.Now}
}

// Allow records an event for key and reports whether it is within the
// limit; otherwise it returns how long until the next event is allowed.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	hits := l.hits[key]
	i := 0
	for i < len(hits) && !hits[i].After(cutoff) {
		i++
	}
	hits = hits[i:]
	if len(hits) >= l.limit {
		l.hits[key] = hits
		return false, hits[0].Sub(cutoff)
	}
	l.hits[key] = append(hits, now)
	if len(l.hits) > 10_000 {
		l.prune(cutoff)
	}
	return true, 0
}

// prune drops keys with no recent events (called with mu held).
func (l *Limiter) prune(cutoff time.Time) {
	for k, hits := range l.hits {
		if len(hits) == 0 || !hits[len(hits)-1].After(cutoff) {
			delete(l.hits, k)
		}
	}
}

// LockoutAfter consecutive failures lock an account.
const LockoutAfter = 5

// LockoutBackoff returns how long to lock an account after failures
// consecutive failed logins: nothing below LockoutAfter, then 1m, 5m, 15m
// and 1h for every further failure.
func LockoutBackoff(failures int) time.Duration {
	switch {
	case failures < LockoutAfter:
		return 0
	case failures == LockoutAfter:
		return time.Minute
	case failures == LockoutAfter+1:
		return 5 * time.Minute
	case failures == LockoutAfter+2:
		return 15 * time.Minute
	}
	return time.Hour
}
