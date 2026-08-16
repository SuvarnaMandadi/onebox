package server

import (
	"net"
	"net/http"
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

// clientIP extracts just the IP from http.Request.RemoteAddr for use as a
// rate-limiter key on pre-auth requests (login/signup, and the public
// chat-share visitor identity — see publicVisitorBillingID). RemoteAddr is
// "ip:port", and the port is a fresh ephemeral value on every single TCP
// connection a real client makes — keying the limiter on the raw string
// (as this file's callers originally did) means the same caller almost
// never repeats a key, so the limit silently never triggers outside of
// tests, where net/http/httptest happens to default every synthetic
// request to the identical RemoteAddr and masks the bug. Falls back to the
// raw value if it isn't in host:port form (e.g. some test doubles), which
// just reproduces the old (safe, if imprecise) behavior rather than
// panicking.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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
