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

// userRateLimitKey renders the bucket key for an authenticated principal.
//
// ok is false when the principal has no renderable id, and the second return
// value is the point: rl.allow(userRateLimitKey(u)) does not compile, so a
// caller cannot bucket an unidentified principal under "" by omission.
func userRateLimitKey(u AuthUser) (string, bool) {
	key := uuidStr(u.ID)
	return key, key != ""
}

// UserRateLimit returns middleware that limits each AUTHENTICATED USER to
// `limit` requests per `window`, answering 429 with a Retry-After on breach.
//
// THE KEY IS THE PRINCIPAL BearerAuth RESOLVED, NOT THE SOURCE ADDRESS, and
// RateLimit's RemoteAddr argument above does not transfer. The address is the
// only identifier that exists before a principal does, which is what makes it
// right ahead of authentication; it is not an identifier the caller cannot
// choose, since an IPv6 caller holds a whole /64 and clientIP keys per /128.
// After authentication there is a principal, resolved
// server-side from a token-hash lookup and not selectable by the caller at all.
// On a SELF-SCOPED route - one that takes every identifier from the context
// principal - the principal charged is the principal the work is done to, so the
// bucket is also the unit the bounded cost belongs to. Where one principal may
// act on another's resource that does not hold: the bucket is charged to the
// caller while the work is charged to the owner. So the argument that carries
// every mount is the operational one. Keyed on the address, one office egress or
// load balancer collapses a whole studio into a single bucket, while one user
// with a workstation and a laptop gets two.
//
// IT MUST BE MOUNTED INSIDE THE AUTH CHAIN and outside any admin gate:
// auth(userLimit(admin(h))). Inside admin, a non-admin's rejected probes are
// free; outside it they are charged to the prober's own bucket. The cost of
// being inside auth is real and is not hidden: a refused request has already
// paid one GetTokenWithUser round trip, so this bounds repetition of expensive
// work and does not bound the auth lookup or request volume.
//
// A request carrying no renderable principal is REFUSED with 401, never passed
// through and never bucketed under "". This middleware is only correct inside
// the auth chain, so such a request is a wiring fault.
//
// THE 401 RETURNS BEFORE rl.allow, WHICH IS WHY THE MAP IS BOUNDED BY THE USER
// TABLE. An unauthenticated caller, or one arriving through a mis-wired chain,
// creates no key at all, so the number of live buckets is the number of distinct
// principals that have actually been resolved. Keying on the presented token
// instead would let anyone mint a fresh key per request; that is the reason this
// is keyed on the resolved user id, and the ordering here is what delivers it.
func UserRateLimit(limit int, window time.Duration) func(http.Handler) http.Handler {
	rl := &rateLimiter{
		windows: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
	}
	go rl.gcLoop()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// The absent-principal case needs no separate arm: UserFromCtx
			// yields the zero AuthUser when its type assertion fails, and a zero
			// AuthUser has no renderable id, so the key check below refuses it
			// too. Input does reach a second arm on the discarded bool; what it
			// cannot do is make that arm answer differently.
			u, _ := UserFromCtx(r.Context())
			key, ok := userRateLimitKey(u)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			retry, allowed := rl.allow(key)
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
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
