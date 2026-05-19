package admin

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a per-IP fixed-window counter. Buckets reset when the
// window expires. Multi-replica deploys get N× the configured rate;
// fine for the current scale (one or two replicas behind a load
// balancer). Implemented locally rather than pulling in golang.org/x/time
// to keep the dependency surface small.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
	limit   int
	window  time.Duration
}

type ipBucket struct {
	count       int
	windowStart time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		buckets: make(map[string]*ipBucket),
		limit:   limit,
		window:  window,
	}
}

// allow returns true if the request from ip is within the limit.
// Concurrent calls are safe.
func (r *rateLimiter) allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	b, ok := r.buckets[ip]
	if !ok || now.Sub(b.windowStart) >= r.window {
		r.buckets[ip] = &ipBucket{count: 1, windowStart: now}
		// Opportunistic GC: every time we mint a new bucket, evict any
		// older than 2× window so the map doesn't grow without bound.
		r.gcOldLocked(now)
		return true
	}
	if b.count >= r.limit {
		return false
	}
	b.count++
	return true
}

func (r *rateLimiter) gcOldLocked(now time.Time) {
	cutoff := now.Add(-2 * r.window)
	for ip, b := range r.buckets {
		if b.windowStart.Before(cutoff) {
			delete(r.buckets, ip)
		}
	}
}

// clientIP returns the request's client IP. Honors X-Forwarded-For if
// the operator's reverse proxy sets it; otherwise falls back to
// RemoteAddr. We trust the first value because the admin UI is meant
// to sit behind a known proxy.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

