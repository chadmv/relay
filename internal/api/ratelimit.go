package api

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ParseRateLimit parses "N:duration" (e.g. "10:1m", "5:30s"). N must be > 0
// and the duration must be > 0. Returns a useful error on malformed input.
func ParseRateLimit(s string) (int, time.Duration, error) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("ratelimit: expected N:duration, got %q", s)
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return 0, 0, fmt.Errorf("ratelimit: count must be a positive integer, got %q", parts[0])
	}
	d, err := time.ParseDuration(parts[1])
	if err != nil || d <= 0 {
		return 0, 0, fmt.Errorf("ratelimit: window must be a positive duration, got %q", parts[1])
	}
	return n, d, nil
}

// rateLimiter tracks recent hit timestamps per key (IP) under a sliding window.
type rateLimiter struct {
	mu      sync.Mutex
	windows map[string][]time.Time
	limit   int
	window  time.Duration
}

// RateLimit returns middleware that limits each client IP to `limit` requests
// per `window`. On breach it returns 429 with a Retry-After header indicating
// how many seconds until the oldest hit falls out of the window. A background
// goroutine prunes empty entries every 5 minutes to bound memory.
//
// RemoteAddr is used directly — X-Forwarded-For is NOT trusted.
func RateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	rl := &rateLimiter{
		windows: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
	}
	go rl.gcLoop()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			retry, ok := rl.allow(ip)
			if !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// allow returns (retryAfter, true) if the hit is allowed or (retryAfter, false)
// if the key is over-limit. retryAfter is only meaningful when false.
func (rl *rateLimiter) allow(key string) (time.Duration, bool) {
	now := time.Now()
	cutoff := now.Add(-rl.window)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	hits := rl.windows[key]
	// Prune old.
	i := 0
	for i < len(hits) && hits[i].Before(cutoff) {
		i++
	}
	hits = hits[i:]

	if len(hits) >= rl.limit {
		retry := rl.window - now.Sub(hits[0])
		rl.windows[key] = hits
		return retry, false
	}
	hits = append(hits, now)
	rl.windows[key] = hits
	return 0, true
}

func (rl *rateLimiter) gcLoop() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		rl.gcOnce(time.Now())
	}
}

func (rl *rateLimiter) gcOnce(now time.Time) {
	cutoff := now.Add(-rl.window)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for key, hits := range rl.windows {
		i := 0
		for i < len(hits) && hits[i].Before(cutoff) {
			i++
		}
		if i == len(hits) {
			delete(rl.windows, key)
		} else {
			rl.windows[key] = hits[i:]
		}
	}
}
