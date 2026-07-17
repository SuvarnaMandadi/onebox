package server

import (
	"sync"
	"time"
)

// rateLimiter is a simple in-memory per-user fixed-window limiter: at most
// perMinute calls to Allow succeed per rolling 60s window. In-memory and
// per-node is a deliberate v0.1 fit with the single-node anti-scope —
// state resets on restart, which is an acceptable trade-off at this
// scale.
type rateLimiter struct {
	mu        sync.Mutex
	perMinute int
	windows   map[string][]time.Time
}

func newRateLimiter(perMinute int) *rateLimiter {
	return &rateLimiter{perMinute: perMinute, windows: make(map[string][]time.Time)}
}

// Allow reports whether userID may make another call right now, and
// records the call if so. perMinute <= 0 means unlimited.
func (r *rateLimiter) Allow(userID string) bool {
	if r.perMinute <= 0 {
		return true
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Minute)

	kept := r.windows[userID][:0]
	for _, t := range r.windows[userID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= r.perMinute {
		r.windows[userID] = kept
		return false
	}
	r.windows[userID] = append(kept, now)
	return true
}
