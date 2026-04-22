# Security Hardening Pass 2 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close four v1 security gaps identified in prior design review — unauthenticated agent registration, missing rate limits on auth endpoints, missing CORS policy, and bootstrap password lingering in process env.

**Architecture:** Four independent changes bundled for one rollout. Agent enrollment uses per-agent long-lived bearer tokens minted from admin-created enrollment tokens (SHA-256 hashed, stored in DB). Rate limiting is in-memory per-IP sliding window middleware. CORS is opt-in allowlist middleware. Bootstrap password is `os.Unsetenv`'d after consumption.

**Tech Stack:** Go 1.22, PostgreSQL via pgx/v5, sqlc for store layer, protobuf+gRPC for agent protocol, Go stdlib `net/http` for REST, Go stdlib `flag` for CLI.

**Rollout order (each step leaves main shippable):**
1. Tasks 1–2: CORS middleware (safest, zero behavior change with empty default)
2. Tasks 3–4: Rate limiting middleware (additive, happy path unaffected)
3. Task 5: Bootstrap password `Unsetenv` (independent, two-line change)
4. Tasks 6–15: Agent enrollment/auth (breaking change gated by Task 14)

**Critical ordering note for Tasks 13–14:** These introduce the only breaking change. Task 13 (agent sends credential) and Task 14 (server enforces) must merge together — if only 13 lands, agents fail to start without credentials; if only 14 lands, agents are rejected for missing credentials they have no way to supply. Merge both before restarting any production agent.

**Running the test suite:**

```bash
# Unit tests — no Docker required
go test ./... -timeout 30s

# Integration tests — requires Docker Desktop running
go test -tags integration -p 1 ./... -timeout 300s

# Regenerate sqlc / protobuf after editing .sql or .proto files
make generate
```

---

## Task 1: CORS middleware

**Files:**
- Create: `internal/api/cors.go`
- Create: `internal/api/cors_test.go`

- [ ] **Step 1.1: Write the failing test file**

`internal/api/cors_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS_EmptyAllowlistEmitsNoHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS(nil)(next)

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin header, got %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestCORS_AllowlistedOriginReceivesHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS([]string{"https://dashboard.studio.local"})(next)

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Origin", "https://dashboard.studio.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.studio.local" {
		t.Fatalf("expected echoed Allow-Origin, got %q", got)
	}
}

func TestCORS_NonAllowlistedOriginGetsNoHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS([]string{"https://dashboard.studio.local"})(next)

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin header, got %q", got)
	}
}

func TestCORS_PreflightReturnsFullHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler should not be called for preflight")
	})
	h := CORS([]string{"https://dashboard.studio.local"})(next)

	req := httptest.NewRequest("OPTIONS", "/v1/jobs", nil)
	req.Header.Set("Origin", "https://dashboard.studio.local")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.studio.local" {
		t.Fatalf("expected Allow-Origin, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatalf("expected Allow-Methods header")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Fatalf("expected Allow-Headers header")
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("expected Max-Age=600, got %q", got)
	}
}

func TestCORS_PreflightNonAllowlistedOrigin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("next handler should not be called for preflight")
	})
	h := CORS([]string{"https://dashboard.studio.local"})(next)

	req := httptest.NewRequest("OPTIONS", "/v1/jobs", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no Allow-Origin, got %q", got)
	}
}

func TestCORS_NeverEmitsAllowCredentials(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := CORS([]string{"https://dashboard.studio.local"})(next)

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Origin", "https://dashboard.studio.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("expected no Allow-Credentials header, got %q", got)
	}
}

func TestParseCORSOrigins(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"single", "https://a.example.com", []string{"https://a.example.com"}, false},
		{"multiple", "https://a.example.com,https://b.example.com", []string{"https://a.example.com", "https://b.example.com"}, false},
		{"whitespace trimmed", " https://a.example.com , https://b.example.com ", []string{"https://a.example.com", "https://b.example.com"}, false},
		{"dedup", "https://a.example.com,https://a.example.com", []string{"https://a.example.com"}, false},
		{"empty entries ignored", "https://a.example.com,,https://b.example.com", []string{"https://a.example.com", "https://b.example.com"}, false},
		{"wildcard rejected", "*", nil, true},
		{"wildcard with others rejected", "https://a.example.com,*", nil, true},
		{"non-http scheme rejected", "ftp://a.example.com", nil, true},
		{"no scheme rejected", "example.com", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCORSOrigins(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v got err=%v", tt.wantErr, err)
			}
			if err != nil {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %v want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("[%d]: got %q want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
```

- [ ] **Step 1.2: Run the tests to verify they fail**

Run: `go test ./internal/api/ -run TestCORS -v`
Expected: FAIL with "undefined: CORS" and "undefined: ParseCORSOrigins"

- [ ] **Step 1.3: Implement the middleware and parser**

`internal/api/cors.go`:

```go
package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ParseCORSOrigins parses a comma-separated allowlist string. Entries are
// trimmed, deduplicated, and validated. Wildcard ("*") is rejected — wildcard
// plus the Authorization header is always a security mistake for this API.
// Empty entries are ignored. Non-HTTP(S) schemes are rejected.
func ParseCORSOrigins(s string) ([]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, raw := range strings.Split(s, ",") {
		origin := strings.TrimSpace(raw)
		if origin == "" {
			continue
		}
		if origin == "*" {
			return nil, fmt.Errorf("cors: wildcard origin (*) is not permitted")
		}
		u, err := url.Parse(origin)
		if err != nil {
			return nil, fmt.Errorf("cors: invalid origin %q: %w", origin, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("cors: origin %q must use http or https scheme", origin)
		}
		if u.Host == "" {
			return nil, fmt.Errorf("cors: origin %q missing host", origin)
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		out = append(out, origin)
	}
	return out, nil
}

// CORS returns middleware emitting CORS headers only for origins in the
// allowlist. Empty allowlist → no CORS headers ever (same-origin only).
// Never emits Access-Control-Allow-Credentials (bearer tokens ride in
// Authorization headers, not cookies).
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	set := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		set[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			_, allowed := set[origin]
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if allowed {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
					w.Header().Set("Access-Control-Max-Age", "600")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}
			next.ServeHTTP(w, r)
		})
	}
}
```

- [ ] **Step 1.4: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run TestCORS -v && go test ./internal/api/ -run TestParseCORSOrigins -v`
Expected: All tests PASS.

- [ ] **Step 1.5: Commit**

```bash
git add internal/api/cors.go internal/api/cors_test.go
git commit -m "feat(api): add CORS middleware with allowlist parsing"
```

---

## Task 2: Wire CORS into server and document env var

**Files:**
- Modify: `internal/api/server.go` (add `CORSOrigins` field to Server, wrap handler)
- Modify: `cmd/relay-server/main.go` (parse `RELAY_CORS_ORIGINS`, pass to api.New)
- Modify: `CLAUDE.md` (add env var row)
- Modify: `README.md` (add env var row)

- [ ] **Step 2.1: Write failing integration test for server wiring**

Append to `internal/api/server_test.go` (create the file if it doesn't exist — check first with `ls internal/api/server_test.go`):

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServer_AppliesCORSWhenConfigured(t *testing.T) {
	s := &Server{CORSOrigins: []string{"https://dashboard.studio.local"}}
	h := s.Handler()

	req := httptest.NewRequest("OPTIONS", "/v1/health", nil)
	req.Header.Set("Origin", "https://dashboard.studio.local")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.studio.local" {
		t.Fatalf("expected CORS origin echoed, got %q", got)
	}
}

func TestServer_NoCORSByDefault(t *testing.T) {
	s := &Server{}
	h := s.Handler()

	req := httptest.NewRequest("GET", "/v1/health", nil)
	req.Header.Set("Origin", "https://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("expected no CORS header, got %q", got)
	}
}
```

- [ ] **Step 2.2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestServer_ -v`
Expected: FAIL (CORSOrigins field doesn't exist on Server).

- [ ] **Step 2.3: Modify `internal/api/server.go`**

Replace the Server struct and `New` / `Handler` sections:

```go
// Server holds shared dependencies for all HTTP handlers.
type Server struct {
	pool        *pgxpool.Pool
	q           *store.Queries
	broker      *events.Broker
	registry    *worker.Registry
	CORSOrigins []string
}

// New creates a Server.
func New(
	pool *pgxpool.Pool,
	q *store.Queries,
	broker *events.Broker,
	registry *worker.Registry,
	corsOrigins []string,
) *Server {
	return &Server{
		pool:        pool,
		q:           q,
		broker:      broker,
		registry:    registry,
		CORSOrigins: corsOrigins,
	}
}
```

At the end of `Handler()`, replace `return mux` with:

```go
	return CORS(s.CORSOrigins)(mux)
}
```

- [ ] **Step 2.4: Update `cmd/relay-server/main.go`**

Add after the `dbMaxConns` block and before the `cfg, err := pgxpool.ParseConfig(dsn)` line:

```go
	corsOrigins, err := api.ParseCORSOrigins(os.Getenv("RELAY_CORS_ORIGINS"))
	if err != nil {
		log.Fatalf("parse RELAY_CORS_ORIGINS: %v", err)
	}
```

Change the `api.New(...)` call to:

```go
	httpServer := api.New(pool, q, broker, registry, corsOrigins)
```

- [ ] **Step 2.5: Update any callers of `api.New` in tests**

Run: `grep -rn "api.New(" --include='*.go'`

Every call site needs `, nil` (or `, []string{...}`) appended. Typical fix in tests:

```go
// before
srv := api.New(pool, q, broker, registry)
// after
srv := api.New(pool, q, broker, registry, nil)
```

Apply to every match.

- [ ] **Step 2.6: Run tests to verify pass**

```bash
go build ./...
go test ./internal/api/ -run TestServer_ -v
go test ./... -timeout 60s
```

Expected: all pass.

- [ ] **Step 2.7: Update `CLAUDE.md`**

In the "Environment Variables (relay-server)" table (under `## Commands`), add a row after `RELAY_WORKER_GRACE_WINDOW`:

```markdown
| `RELAY_CORS_ORIGINS` | _(empty)_ | Comma-separated CORS allowlist for HTTP API (empty = same-origin only, wildcard `*` rejected) |
```

- [ ] **Step 2.8: Update `README.md`**

In the env var table (search for `RELAY_BOOTSTRAP_ADMIN` to find the table), add the same row.

- [ ] **Step 2.9: Commit**

```bash
git add internal/api/server.go internal/api/server_test.go cmd/relay-server/main.go CLAUDE.md README.md
# stage any other files modified to update api.New call sites
git add internal/api/
git commit -m "feat(server): wire CORS middleware via RELAY_CORS_ORIGINS"
```

---

## Task 3: Rate limiter middleware

**Files:**
- Create: `internal/api/ratelimit.go`
- Create: `internal/api/ratelimit_test.go`

- [ ] **Step 3.1: Write the failing test file**

`internal/api/ratelimit_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestParseRateLimit(t *testing.T) {
	tests := []struct {
		in       string
		wantN    int
		wantWin  time.Duration
		wantErr  bool
	}{
		{"10:1m", 10, time.Minute, false},
		{"5:30s", 5, 30 * time.Second, false},
		{"100:1h", 100, time.Hour, false},
		{"0:1m", 0, 0, true},     // count must be > 0
		{"10:0s", 0, 0, true},    // window must be > 0
		{"nonsense", 0, 0, true},
		{"10", 0, 0, true},       // missing separator
		{"", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			n, w, err := ParseRateLimit(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("wantErr=%v err=%v", tt.wantErr, err)
			}
			if tt.wantErr {
				return
			}
			if n != tt.wantN || w != tt.wantWin {
				t.Fatalf("got %d,%s want %d,%s", n, w, tt.wantN, tt.wantWin)
			}
		})
	}
}

func TestRateLimit_UnderLimitPasses(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(3, time.Minute)(next)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("POST", "/x", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: got %d want 200", i+1, rec.Code)
		}
	}
}

func TestRateLimit_OverLimitReturns429WithRetryAfter(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(2, time.Minute)(next)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/x", nil)
		req.RemoteAddr = "10.0.0.1:12345"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	req := httptest.NewRequest("POST", "/x", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	ra := rec.Header().Get("Retry-After")
	if ra == "" {
		t.Fatalf("expected Retry-After header")
	}
	secs, err := strconv.Atoi(ra)
	if err != nil || secs < 1 {
		t.Fatalf("expected positive integer Retry-After, got %q", ra)
	}
}

func TestRateLimit_PerIPIsolation(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(1, time.Minute)(next)

	req1 := httptest.NewRequest("POST", "/x", nil)
	req1.RemoteAddr = "10.0.0.1:12345"
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("IP1 first: got %d", rec1.Code)
	}

	req2 := httptest.NewRequest("POST", "/x", nil)
	req2.RemoteAddr = "10.0.0.2:12345"
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("IP2 first: got %d", rec2.Code)
	}

	// IP1 second should be blocked
	req1b := httptest.NewRequest("POST", "/x", nil)
	req1b.RemoteAddr = "10.0.0.1:54321"
	rec1b := httptest.NewRecorder()
	h.ServeHTTP(rec1b, req1b)
	if rec1b.Code != http.StatusTooManyRequests {
		t.Fatalf("IP1 second: expected 429, got %d", rec1b.Code)
	}
}

func TestRateLimit_WindowSlides(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(1, 50*time.Millisecond)(next)

	req := httptest.NewRequest("POST", "/x", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	h.ServeHTTP(httptest.NewRecorder(), req)

	// Immediately second should 429
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec2.Code)
	}

	// Wait past the window
	time.Sleep(75 * time.Millisecond)

	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req)
	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 after window slide, got %d", rec3.Code)
	}
}

func TestRateLimit_ConcurrentHitsDontRace(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := RateLimit(100, time.Minute)(next)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("POST", "/x", nil)
			req.RemoteAddr = "10.0.0." + strconv.Itoa(i%10) + ":12345"
			h.ServeHTTP(httptest.NewRecorder(), req)
		}(i)
	}
	wg.Wait()
}
```

- [ ] **Step 3.2: Run to verify failure**

Run: `go test ./internal/api/ -run TestRateLimit -v -race`
Expected: FAIL with "undefined: RateLimit" / "undefined: ParseRateLimit".

- [ ] **Step 3.3: Implement the rate limiter**

`internal/api/ratelimit.go`:

```go
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
```

- [ ] **Step 3.4: Run tests to verify pass**

Run: `go test ./internal/api/ -run TestRateLimit -v -race && go test ./internal/api/ -run TestParseRateLimit -v`
Expected: all tests PASS, no data race.

- [ ] **Step 3.5: Commit**

```bash
git add internal/api/ratelimit.go internal/api/ratelimit_test.go
git commit -m "feat(api): add per-IP sliding-window rate limiter middleware"
```

---

## Task 4: Apply rate limiter to login/register + wire env vars

**Files:**
- Modify: `internal/api/server.go` (fields for login/register limiters, apply to routes)
- Modify: `cmd/relay-server/main.go` (parse env vars, pass to api.New)
- Modify: `CLAUDE.md`
- Modify: `README.md`

- [ ] **Step 4.1: Write the failing integration test**

Append to `internal/api/server_test.go`:

```go
func TestServer_RateLimitsLogin(t *testing.T) {
	s := &Server{
		LoginLimitN:    2,
		LoginLimitWin:  time.Minute,
	}
	h := s.Handler()

	// Three login attempts from the same IP; the third should 429.
	body := `{"email":"a@b.co","password":"x"}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(body))
		req.RemoteAddr = "10.0.0.1:12345"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		// Response code doesn't matter — the handler will fail DB lookup. We
		// only care that it's NOT 429.
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d: unexpected 429", i+1)
		}
	}

	req := httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on third login, got %d", rec.Code)
	}
}
```

Add `"strings"` and `"time"` to the imports if not already present.

- [ ] **Step 4.2: Run to verify failure**

Run: `go test ./internal/api/ -run TestServer_RateLimitsLogin -v`
Expected: FAIL (fields `LoginLimitN`/`LoginLimitWin` don't exist).

- [ ] **Step 4.3: Modify `internal/api/server.go`**

Extend the Server struct:

```go
type Server struct {
	pool            *pgxpool.Pool
	q               *store.Queries
	broker          *events.Broker
	registry        *worker.Registry
	CORSOrigins     []string
	LoginLimitN     int
	LoginLimitWin   time.Duration
	RegisterLimitN  int
	RegisterLimitWin time.Duration
}
```

Add `"time"` to the imports.

Update `New` signature to accept the four new values:

```go
func New(
	pool *pgxpool.Pool,
	q *store.Queries,
	broker *events.Broker,
	registry *worker.Registry,
	corsOrigins []string,
	loginLimitN int,
	loginLimitWin time.Duration,
	registerLimitN int,
	registerLimitWin time.Duration,
) *Server {
	return &Server{
		pool:             pool,
		q:                q,
		broker:           broker,
		registry:         registry,
		CORSOrigins:      corsOrigins,
		LoginLimitN:      loginLimitN,
		LoginLimitWin:    loginLimitWin,
		RegisterLimitN:   registerLimitN,
		RegisterLimitWin: registerLimitWin,
	}
}
```

In `Handler()`, change the two public auth route registrations. Default `limit=0` or `window=0` means "no limit" (middleware is skipped), so tests constructing `&Server{}` directly still work:

```go
	// Public endpoints
	mux.HandleFunc("GET /v1/health", s.handleHealth)

	loginH := http.HandlerFunc(s.handleRegister)
	if s.RegisterLimitN > 0 && s.RegisterLimitWin > 0 {
		mux.Handle("POST /v1/auth/register", RateLimit(s.RegisterLimitN, s.RegisterLimitWin)(loginH))
	} else {
		mux.HandleFunc("POST /v1/auth/register", s.handleRegister)
	}

	if s.LoginLimitN > 0 && s.LoginLimitWin > 0 {
		mux.Handle("POST /v1/auth/login", RateLimit(s.LoginLimitN, s.LoginLimitWin)(http.HandlerFunc(s.handleLogin)))
	} else {
		mux.HandleFunc("POST /v1/auth/login", s.handleLogin)
	}
```

(Remove the two original `mux.HandleFunc("POST /v1/auth/register", ...)` / `mux.HandleFunc("POST /v1/auth/login", ...)` lines.)

- [ ] **Step 4.4: Update `cmd/relay-server/main.go`**

Add parsing after the CORS parse, before `api.New`:

```go
	loginN, loginWin, err := api.ParseRateLimit(envOrDefault("RELAY_LOGIN_RATE_LIMIT", "10:1m"))
	if err != nil {
		log.Fatalf("parse RELAY_LOGIN_RATE_LIMIT: %v", err)
	}
	registerN, registerWin, err := api.ParseRateLimit(envOrDefault("RELAY_REGISTER_RATE_LIMIT", "5:1m"))
	if err != nil {
		log.Fatalf("parse RELAY_REGISTER_RATE_LIMIT: %v", err)
	}
```

And add this helper at the bottom of `cmd/relay-server/main.go`:

```go
func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
```

Update the `api.New` call:

```go
	httpServer := api.New(pool, q, broker, registry, corsOrigins, loginN, loginWin, registerN, registerWin)
```

- [ ] **Step 4.5: Update any other `api.New` call sites**

Run: `grep -rn "api.New(" --include='*.go'`

Every test or fixture that calls `api.New` needs four additional zero-value args (`, 0, 0, 0, 0`) to disable rate limiting in that context.

- [ ] **Step 4.6: Run all tests**

```bash
go build ./...
go test ./... -timeout 60s -race
```

Expected: all pass.

- [ ] **Step 4.7: Update docs**

`CLAUDE.md` env var table — add two rows:

```markdown
| `RELAY_LOGIN_RATE_LIMIT` | `10:1m` | Per-IP rate limit for `POST /v1/auth/login` (format `N:duration`) |
| `RELAY_REGISTER_RATE_LIMIT` | `5:1m` | Per-IP rate limit for `POST /v1/auth/register` |
```

Same two rows in `README.md`.

- [ ] **Step 4.8: Commit**

```bash
git add internal/api/server.go internal/api/server_test.go cmd/relay-server/main.go CLAUDE.md README.md
git add internal/api/  # any call-site fixes
git commit -m "feat(server): rate-limit /v1/auth/login and /v1/auth/register"
```

---

## Task 5: Unsetenv bootstrap password after use

**Files:**
- Modify: `cmd/relay-server/main.go:70-78`
- Modify: `CLAUDE.md`
- Modify: `README.md`

- [ ] **Step 5.1: Edit `cmd/relay-server/main.go`**

Replace lines 70–78 with:

```go
	if bootstrapEmail := os.Getenv("RELAY_BOOTSTRAP_ADMIN"); bootstrapEmail != "" {
		bootstrapPassword := os.Getenv("RELAY_BOOTSTRAP_PASSWORD")
		if bootstrapPassword == "" {
			log.Fatalf("RELAY_BOOTSTRAP_PASSWORD must be set when RELAY_BOOTSTRAP_ADMIN is set")
		}
		if err := bootstrapAdmin(ctx, q, bootstrapEmail, bootstrapPassword); err != nil {
			log.Fatalf("bootstrap admin: %v", err)
		}
		// Clear from process env so it's not visible via /proc/<pid>/environ or
		// inherited by any future child process. The string itself may linger
		// in heap memory until GC; see docs/superpowers/specs/... residual
		// risks table. This is best-effort.
		os.Unsetenv("RELAY_BOOTSTRAP_PASSWORD")
		os.Unsetenv("RELAY_BOOTSTRAP_ADMIN")
		bootstrapPassword = ""
		_ = bootstrapPassword
	}
```

- [ ] **Step 5.2: Update docs**

`CLAUDE.md` — replace the existing two bootstrap rows with:

```markdown
| `RELAY_BOOTSTRAP_ADMIN` | — | Email of first admin to create on startup. Cleared from process env after consumption. |
| `RELAY_BOOTSTRAP_PASSWORD` | — | Required when `RELAY_BOOTSTRAP_ADMIN` is set. Cleared from process env after consumption; operators should also unset it from their shell. |
```

`README.md` — make the equivalent change in its env table.

- [ ] **Step 5.3: Build and run existing tests**

```bash
go build ./...
go test ./cmd/... -timeout 30s
```

Expected: all pass.

- [ ] **Step 5.4: Commit**

```bash
git add cmd/relay-server/main.go CLAUDE.md README.md
git commit -m "fix(server): clear bootstrap credentials from process env after use"
```

---

## Task 6: Migration — agent_enrollments table + agent_token_hash column

**Files:**
- Create: `internal/store/migrations/000005_agent_auth.up.sql`
- Create: `internal/store/migrations/000005_agent_auth.down.sql`

- [ ] **Step 6.1: Create the up migration**

`internal/store/migrations/000005_agent_auth.up.sql`:

```sql
CREATE TABLE agent_enrollments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash      TEXT NOT NULL UNIQUE,
    hostname_hint   TEXT,
    created_by      UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    consumed_at     TIMESTAMPTZ,
    consumed_by     UUID REFERENCES workers(id)
);
CREATE INDEX ix_agent_enrollments_token_hash ON agent_enrollments(token_hash);

ALTER TABLE workers ADD COLUMN agent_token_hash TEXT UNIQUE;
CREATE INDEX ix_workers_agent_token_hash
  ON workers(agent_token_hash) WHERE agent_token_hash IS NOT NULL;
```

- [ ] **Step 6.2: Create the down migration**

`internal/store/migrations/000005_agent_auth.down.sql`:

```sql
DROP INDEX IF EXISTS ix_workers_agent_token_hash;
ALTER TABLE workers DROP COLUMN IF EXISTS agent_token_hash;

DROP INDEX IF EXISTS ix_agent_enrollments_token_hash;
DROP TABLE IF EXISTS agent_enrollments;
```

- [ ] **Step 6.3: Verify migration applies cleanly**

Start an ephemeral postgres:

```bash
docker run -d --rm --name relay-mig-test -e POSTGRES_USER=relay -e POSTGRES_PASSWORD=relay -e POSTGRES_DB=relay -p 5433:5432 postgres:15
sleep 3
RELAY_DATABASE_URL=postgres://relay:relay@localhost:5433/relay?sslmode=disable go test -tags integration ./internal/store/ -run TestMigrate -v -timeout 60s
docker rm -f relay-mig-test
```

(Skip this step if there's no existing `TestMigrate`; build will still validate SQL when tasks 7+ run integration tests.)

- [ ] **Step 6.4: Commit**

```bash
git add internal/store/migrations/000005_agent_auth.up.sql internal/store/migrations/000005_agent_auth.down.sql
git commit -m "feat(store): add agent_enrollments table and workers.agent_token_hash"
```

---

## Task 7: sqlc queries for agent_enrollments

**Files:**
- Create: `internal/store/query/agent_enrollments.sql`
- Regenerate: `internal/store/agent_enrollments.sql.go` (via `make generate`)
- Regenerate: `internal/store/models.go` (via `make generate`)
- Create: `internal/store/agent_enrollments_test.go` (integration)

- [ ] **Step 7.1: Create the SQL query file**

`internal/store/query/agent_enrollments.sql`:

```sql
-- name: CreateAgentEnrollment :one
INSERT INTO agent_enrollments (token_hash, hostname_hint, created_by, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAgentEnrollmentByTokenHash :one
SELECT * FROM agent_enrollments WHERE token_hash = $1;

-- name: ConsumeAgentEnrollment :execrows
UPDATE agent_enrollments
SET consumed_at = NOW(), consumed_by = $2
WHERE id = $1 AND consumed_at IS NULL;

-- name: ListActiveAgentEnrollments :many
SELECT id, hostname_hint, created_by, created_at, expires_at
FROM agent_enrollments
WHERE consumed_at IS NULL AND expires_at > NOW()
ORDER BY created_at DESC;

-- name: DeleteExpiredAgentEnrollments :execrows
DELETE FROM agent_enrollments WHERE expires_at <= NOW() AND consumed_at IS NULL;
```

- [ ] **Step 7.2: Regenerate sqlc bindings**

```bash
make generate
```

Verify the newly generated file exists: `ls internal/store/agent_enrollments.sql.go`

- [ ] **Step 7.3: Write the failing integration test**

`internal/store/agent_enrollments_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"testing"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
)

func TestAgentEnrollments_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)
	admin := newTestUser(t, q, true)

	created, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash:    "abc123",
		HostnameHint: ptrStr("render-node-07"),
		CreatedBy:    admin.ID,
		ExpiresAt:    pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	got, err := q.GetAgentEnrollmentByTokenHash(ctx, "abc123")
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.False(t, got.ConsumedAt.Valid)
}

func TestAgentEnrollments_ConsumeIsSingleShot(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)
	admin := newTestUser(t, q, true)
	worker := newTestWorker(t, q)

	enroll, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "consume1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	rows, err := q.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{
		ID:         enroll.ID,
		ConsumedBy: worker.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)

	// Second consume should affect 0 rows.
	rows2, err := q.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{
		ID:         enroll.ID,
		ConsumedBy: worker.ID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), rows2)
}

func TestAgentEnrollments_ListActiveExcludesConsumedAndExpired(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)
	admin := newTestUser(t, q, true)
	worker := newTestWorker(t, q)

	active, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "active1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	expired, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "expired1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err)

	consumed, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "consumed1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)
	_, err = q.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{ID: consumed.ID, ConsumedBy: worker.ID})
	require.NoError(t, err)

	list, err := q.ListActiveAgentEnrollments(ctx)
	require.NoError(t, err)

	seen := make(map[string]bool)
	for _, e := range list {
		seen[uuidStrLocal(e.ID)] = true
	}
	require.True(t, seen[uuidStrLocal(active.ID)], "active should be listed")
	require.False(t, seen[uuidStrLocal(expired.ID)], "expired should not be listed")
	require.False(t, seen[uuidStrLocal(consumed.ID)], "consumed should not be listed")
}

func TestAgentEnrollments_DeleteExpired(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)
	admin := newTestUser(t, q, true)

	_, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "old1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true},
	})
	require.NoError(t, err)
	_, err = q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: "fresh1",
		CreatedBy: admin.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
	})
	require.NoError(t, err)

	rows, err := q.DeleteExpiredAgentEnrollments(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), rows)
}

func ptrStr(s string) *string { return &s }

func uuidStrLocal(u pgtype.UUID) string {
	b := u.Bytes
	return string([]byte{b[0], b[1], b[2], b[3]})
}
```

**Helper reference** — `newTestStore`, `newTestUser`, `newTestWorker` should already exist in `internal/store/store_test.go`. If any is missing, add it there following the existing patterns in that file. Check first: `grep -n "newTestStore\|newTestUser\|newTestWorker" internal/store/*_test.go`.

- [ ] **Step 7.4: Run the test to verify it passes**

```bash
go test -tags integration -p 1 ./internal/store/ -run TestAgentEnrollments -v -timeout 120s
```

Expected: all four tests PASS.

- [ ] **Step 7.5: Commit**

```bash
git add internal/store/query/agent_enrollments.sql internal/store/agent_enrollments.sql.go internal/store/models.go internal/store/agent_enrollments_test.go
git commit -m "feat(store): add agent_enrollments queries"
```

---

## Task 8: sqlc queries for worker agent tokens

**Files:**
- Modify: `internal/store/query/workers.sql`
- Regenerate: `internal/store/workers.sql.go`
- Modify: `internal/store/workers_test.go` (or create `internal/store/workers_token_test.go`)

- [ ] **Step 8.1: Extend the SQL query file**

Append to `internal/store/query/workers.sql`:

```sql
-- name: SetWorkerAgentToken :exec
UPDATE workers SET agent_token_hash = $2 WHERE id = $1;

-- name: ClearWorkerAgentToken :exec
UPDATE workers
SET agent_token_hash = NULL, status = 'revoked'
WHERE id = $1;

-- name: GetWorkerByAgentTokenHash :one
SELECT * FROM workers
WHERE agent_token_hash = $1 AND status != 'revoked';
```

- [ ] **Step 8.2: Regenerate**

```bash
make generate
```

- [ ] **Step 8.3: Write the failing integration test**

Create `internal/store/workers_token_test.go`:

```go
//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"

	"relay/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestWorkerAgentToken_SetAndGet(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)
	w := newTestWorker(t, q)

	err := q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID:             w.ID,
		AgentTokenHash: ptrStr("hash-abc"),
	})
	require.NoError(t, err)

	got, err := q.GetWorkerByAgentTokenHash(ctx, ptrStr("hash-abc"))
	require.NoError(t, err)
	require.Equal(t, w.ID, got.ID)
}

func TestWorkerAgentToken_ClearSetsRevokedAndBlocksLookup(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)
	w := newTestWorker(t, q)

	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID:             w.ID,
		AgentTokenHash: ptrStr("hash-xyz"),
	}))

	require.NoError(t, q.ClearWorkerAgentToken(ctx, w.ID))

	_, err := q.GetWorkerByAgentTokenHash(ctx, ptrStr("hash-xyz"))
	require.Error(t, err)
	require.True(t, errors.Is(err, pgx.ErrNoRows))

	// Status row should now be "revoked".
	reloaded, err := q.GetWorker(ctx, w.ID)
	require.NoError(t, err)
	require.Equal(t, "revoked", reloaded.Status)
}

func TestWorkerAgentToken_RevokedWorkerNotFoundByHash(t *testing.T) {
	ctx := context.Background()
	q := newTestStore(t)
	w := newTestWorker(t, q)

	// Bypass ClearWorkerAgentToken to test the defense-in-depth case where
	// status=revoked but hash is still set (shouldn't happen in practice).
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID:             w.ID,
		AgentTokenHash: ptrStr("still-set"),
	}))
	_, err := q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         w.ID,
		Status:     "revoked",
		LastSeenAt: w.LastSeenAt,
	})
	require.NoError(t, err)

	_, err = q.GetWorkerByAgentTokenHash(ctx, ptrStr("still-set"))
	require.Error(t, err)
	require.True(t, errors.Is(err, pgx.ErrNoRows))
}
```

Note: `AgentTokenHash` is typed as `*string` because the column is nullable; sqlc generates pointer types for nullable TEXT. If sqlc instead generates `pgtype.Text`, adjust the tests to construct `pgtype.Text{String: "hash-abc", Valid: true}` and inspect the generated `SetWorkerAgentTokenParams` to pick the correct type.

- [ ] **Step 8.4: Run the test**

```bash
go test -tags integration -p 1 ./internal/store/ -run TestWorkerAgentToken -v -timeout 120s
```

Expected: all three tests PASS. If the test fails to compile due to parameter type mismatch, inspect `internal/store/workers.sql.go` to confirm the exact generated parameter struct and update the test.

- [ ] **Step 8.5: Commit**

```bash
git add internal/store/query/workers.sql internal/store/workers.sql.go internal/store/workers_token_test.go
git commit -m "feat(store): add worker agent-token set/clear/lookup queries"
```

---

## Task 9: Proto additions — credential oneof + agent_token response

**Files:**
- Modify: `proto/relayv1/relay.proto`
- Regenerate: `internal/proto/relayv1/relay.pb.go` (via `make generate`)

- [ ] **Step 9.1: Edit the proto**

Replace the `RegisterRequest` message with:

```proto
// Sent once when the stream opens. worker_id is empty on first registration.
// running_tasks is the agent's list of currently-executing tasks at reconnect
// time (empty on first connect). The coordinator diffs against DB state and
// replies with RegisterResponse.cancel_task_ids for any stale assignments.
//
// Exactly one credential field must be set. enrollment_token is used only on
// first boot of a fresh agent; every subsequent reconnect sends agent_token.
message RegisterRequest {
  string worker_id                 = 1;
  string hostname                  = 2;
  int32  cpu_cores                 = 3;
  int32  ram_gb                    = 4;
  int32  gpu_count                 = 5;
  string gpu_model                 = 6;
  string os                        = 7;
  repeated RunningTask running_tasks = 8;
  oneof credential {
    string enrollment_token = 9;
    string agent_token      = 10;
  }
}
```

Replace the `RegisterResponse` message with:

```proto
// Sent in response to RegisterRequest. agent_token is populated only on
// successful enrollment (first connect with a valid enrollment_token); the
// agent must persist it and send it as agent_token on every reconnect.
// cancel_task_ids lists tasks the agent reported as running that the
// coordinator considers stale.
message RegisterResponse {
  string          worker_id       = 1;
  repeated string cancel_task_ids = 2;
  string          agent_token     = 3;
}
```

- [ ] **Step 9.2: Regenerate bindings**

```bash
make generate
```

Verify: `grep -n "GetEnrollmentToken\|GetAgentToken" internal/proto/relayv1/relay.pb.go | head -5`

Expected: getters for new fields exist.

- [ ] **Step 9.3: Build to verify nothing else broke**

```bash
go build ./...
```

Any compile errors will be in call sites that construct `RegisterRequest` / `RegisterResponse` — tasks 13 and 14 will update those. For now, if there are pre-existing test fixtures that fail to compile from the oneof addition, bundle a minimal compile fix (constructing `&relayv1.RegisterRequest{Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: "test-token"}}`) here so the tree builds. List each file under "Modify" as you touch it.

- [ ] **Step 9.4: Commit**

```bash
git add proto/relayv1/relay.proto internal/proto/relayv1/relay.pb.go
# plus any fixtures you had to touch to keep the tree building
git commit -m "feat(proto): add credential oneof and agent_token response field"
```

---

## Task 10: Admin HTTP endpoints for enrollments and revocation

**Files:**
- Create: `internal/api/agent_enrollments.go`
- Create: `internal/api/agent_enrollments_test.go`
- Modify: `internal/api/server.go` (register routes)

- [ ] **Step 10.1: Write the failing tests**

`internal/api/agent_enrollments_test.go`:

```go
//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"relay/internal/api"
	"relay/internal/events"
	"relay/internal/worker"

	"github.com/stretchr/testify/require"
)

func TestCreateAgentEnrollment_AdminOnly(t *testing.T) {
	ctx := context.Background()
	fx := newAPITestFixture(t)  // existing helper — creates admin + non-admin + server
	defer fx.Cleanup()

	body := []byte(`{"hostname_hint":"render-07","ttl_seconds":3600}`)

	// Non-admin → 403.
	req := httptest.NewRequest("POST", "/v1/agent-enrollments", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+fx.UserToken)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// Admin → 201 with token.
	req2 := httptest.NewRequest("POST", "/v1/agent-enrollments", bytes.NewReader(body))
	req2.Header.Set("Authorization", "Bearer "+fx.AdminToken)
	rec2 := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusCreated, rec2.Code)

	var resp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Token)
	require.NotEmpty(t, resp.ExpiresAt)

	_ = ctx
	_ = events.Event{}
	_ = worker.NewRegistry
}

func TestCreateAgentEnrollment_RejectsInvalidTTL(t *testing.T) {
	fx := newAPITestFixture(t)
	defer fx.Cleanup()

	cases := []struct {
		name string
		body string
	}{
		{"too short", `{"ttl_seconds":30}`},           // min 60s
		{"too long", `{"ttl_seconds":1000000}`},       // max 7d = 604800s
		{"negative", `{"ttl_seconds":-1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/v1/agent-enrollments", bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Authorization", "Bearer "+fx.AdminToken)
			rec := httptest.NewRecorder()
			fx.Handler.ServeHTTP(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code, tc.body)
		})
	}
}

func TestListAgentEnrollments_AdminOnly(t *testing.T) {
	fx := newAPITestFixture(t)
	defer fx.Cleanup()

	// Seed one via admin POST.
	body := []byte(`{"hostname_hint":"a","ttl_seconds":3600}`)
	req := httptest.NewRequest("POST", "/v1/agent-enrollments", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+fx.AdminToken)
	fx.Handler.ServeHTTP(httptest.NewRecorder(), req)

	// Non-admin → 403.
	r2 := httptest.NewRequest("GET", "/v1/agent-enrollments", nil)
	r2.Header.Set("Authorization", "Bearer "+fx.UserToken)
	rec2 := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec2, r2)
	require.Equal(t, http.StatusForbidden, rec2.Code)

	// Admin → 200 with at least one row, no token/hash in response.
	r3 := httptest.NewRequest("GET", "/v1/agent-enrollments", nil)
	r3.Header.Set("Authorization", "Bearer "+fx.AdminToken)
	rec3 := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec3, r3)
	require.Equal(t, http.StatusOK, rec3.Code)

	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec3.Body.Bytes(), &list))
	require.GreaterOrEqual(t, len(list), 1)
	_, hasToken := list[0]["token"]
	require.False(t, hasToken, "token must not be exposed in list")
	_, hasHash := list[0]["token_hash"]
	require.False(t, hasHash, "token_hash must not be exposed in list")
}

func TestDeleteWorkerToken_AdminOnly(t *testing.T) {
	fx := newAPITestFixture(t)
	defer fx.Cleanup()

	// Seed a worker via the store directly (test helper).
	w := fx.NewTestWorker(t)

	url := "/v1/workers/" + uuidStr(w.ID) + "/token"

	// Non-admin → 403.
	r := httptest.NewRequest("DELETE", url, nil)
	r.Header.Set("Authorization", "Bearer "+fx.UserToken)
	rec := httptest.NewRecorder()
	fx.Handler.ServeHTTP(rec, r)
	require.Equal(t, http.StatusForbidden, rec.Code)

	// Admin → 204, idempotent on repeat.
	for i := 0; i < 2; i++ {
		r2 := httptest.NewRequest("DELETE", url, nil)
		r2.Header.Set("Authorization", "Bearer "+fx.AdminToken)
		rec2 := httptest.NewRecorder()
		fx.Handler.ServeHTTP(rec2, r2)
		require.Equal(t, http.StatusNoContent, rec2.Code)
	}
}

func uuidStr(u interface{ Bytes() [16]byte }) string {
	// Prefer the existing uuidStr helper in the api package; adjust if this
	// local stub is unnecessary.
	return ""
}
```

**Test fixture note**: `newAPITestFixture` is a helper that creates a testcontainers postgres, an admin user, a non-admin user, returns tokens for each, and exposes an `http.Handler`. Check the existing `internal/api/*_test.go` files — there is likely an existing fixture pattern (e.g. in `auth_integration_test.go`). If the fixture doesn't exist in a reusable form, extract it first into a helper file `internal/api/fixture_test.go` before writing this task's tests. Reuse over creation — don't duplicate setup code.

- [ ] **Step 10.2: Run to verify failure**

```bash
go test -tags integration -p 1 ./internal/api/ -run TestCreateAgentEnrollment -v -timeout 120s
```

Expected: FAIL (handlers don't exist).

- [ ] **Step 10.3: Implement the handlers**

`internal/api/agent_enrollments.go`:

```go
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"relay/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	defaultEnrollmentTTL = 24 * time.Hour
	minEnrollmentTTL     = time.Minute
	maxEnrollmentTTL     = 7 * 24 * time.Hour
)

// handleCreateAgentEnrollment creates a fresh single-use enrollment token.
// Admin-only. Returns the raw token exactly once.
func (s *Server) handleCreateAgentEnrollment(w http.ResponseWriter, r *http.Request) {
	u, ok := UserFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var req struct {
		HostnameHint string `json:"hostname_hint"`
		TTLSeconds   int64  `json:"ttl_seconds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ttl := defaultEnrollmentTTL
	if req.TTLSeconds != 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	if ttl < minEnrollmentTTL {
		writeError(w, http.StatusBadRequest, "ttl_seconds must be at least 60")
		return
	}
	if ttl > maxEnrollmentTTL {
		writeError(w, http.StatusBadRequest, "ttl_seconds must not exceed 604800 (7 days)")
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate token")
		return
	}
	rawHex := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(rawHex))
	hash := hex.EncodeToString(sum[:])

	params := store.CreateAgentEnrollmentParams{
		TokenHash: hash,
		CreatedBy: u.ID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
	}
	if req.HostnameHint != "" {
		params.HostnameHint = &req.HostnameHint
	}

	row, err := s.q.CreateAgentEnrollment(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create enrollment")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":         uuidStr(row.ID),
		"token":      rawHex,
		"expires_at": row.ExpiresAt.Time,
	})
}

// handleListAgentEnrollments returns non-consumed non-expired enrollments.
// Never returns the token or hash — only metadata.
func (s *Server) handleListAgentEnrollments(w http.ResponseWriter, r *http.Request) {
	rows, err := s.q.ListActiveAgentEnrollments(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list enrollments")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		entry := map[string]any{
			"id":         uuidStr(row.ID),
			"created_at": row.CreatedAt.Time,
			"expires_at": row.ExpiresAt.Time,
			"created_by": uuidStr(row.CreatedBy),
		}
		if row.HostnameHint != nil {
			entry["hostname_hint"] = *row.HostnameHint
		}
		out = append(out, entry)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeleteWorkerToken revokes a worker's agent token. Admin-only.
// Idempotent — always returns 204 on valid worker ID.
func (s *Server) handleDeleteWorkerToken(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid worker id")
		return
	}
	if err := s.q.ClearWorkerAgentToken(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear worker token")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 10.4: Register routes in `internal/api/server.go`**

In `Handler()`, after the existing invites route, add:

```go
	// Agent enrollments (admin-only)
	mux.Handle("POST /v1/agent-enrollments", auth(admin(http.HandlerFunc(s.handleCreateAgentEnrollment))))
	mux.Handle("GET /v1/agent-enrollments", auth(admin(http.HandlerFunc(s.handleListAgentEnrollments))))
	mux.Handle("DELETE /v1/workers/{id}/token", auth(admin(http.HandlerFunc(s.handleDeleteWorkerToken))))
```

- [ ] **Step 10.5: Run tests to verify pass**

```bash
go test -tags integration -p 1 ./internal/api/ -run "TestCreateAgentEnrollment|TestListAgentEnrollments|TestDeleteWorkerToken" -v -timeout 180s
```

Expected: all pass.

- [ ] **Step 10.6: Commit**

```bash
git add internal/api/agent_enrollments.go internal/api/agent_enrollments_test.go internal/api/server.go
git commit -m "feat(api): add admin endpoints for agent enrollment and revocation"
```

---

## Task 11: Admin CLI — `relay agent enroll` and `relay workers revoke`

**Files:**
- Create: `internal/cli/agent_enroll.go`
- Create: `internal/cli/agent_enroll_test.go`
- Create: `internal/cli/workers_revoke.go`
- Create: `internal/cli/workers_revoke_test.go`
- Modify: `cmd/relay/main.go` (register commands)

- [ ] **Step 11.1: Inspect an existing CLI command for patterns**

Run: `cat internal/cli/invites.go` (or any other admin CLI command file) to match the argument-parsing and HTTP-call style used by the rest of the CLI.

- [ ] **Step 11.2: Implement `internal/cli/agent_enroll.go`**

```go
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"
)

// AgentEnroll is the top-level `relay agent` subcommand group. Today it has
// one leaf verb, `enroll`, but is structured to accept others later.
var AgentEnroll = Command{
	Name:  "agent",
	Usage: "Manage agent enrollment tokens (admin)",
	Run:   runAgent,
}

func runAgent(ctx context.Context, args []string, cfg *Config) error {
	if len(args) == 0 {
		fmt.Println("Usage: relay agent <enroll>")
		return silentError{}
	}
	switch args[0] {
	case "enroll":
		return runAgentEnroll(ctx, args[1:], cfg)
	default:
		return fmt.Errorf("unknown agent subcommand: %s", args[0])
	}
}

func runAgentEnroll(ctx context.Context, args []string, cfg *Config) error {
	fs := flag.NewFlagSet("agent enroll", flag.ContinueOnError)
	hostname := fs.String("hostname", "", "hostname hint (informational)")
	ttl := fs.Duration("ttl", 24*time.Hour, "token validity duration")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	client, err := newClient(cfg)
	if err != nil {
		return err
	}

	body := map[string]any{
		"ttl_seconds": int64(ttl.Seconds()),
	}
	if *hostname != "" {
		body["hostname_hint"] = *hostname
	}

	var resp struct {
		ID        string    `json:"id"`
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := client.PostJSON(ctx, "/v1/agent-enrollments", body, &resp); err != nil {
		return err
	}

	// Print the token to stdout alone so it's easy to capture. Metadata to
	// stderr so `relay agent enroll | some-pipeline` works cleanly.
	fmt.Fprintf(stderr, "enrollment expires at %s\n", resp.ExpiresAt.Format(time.RFC3339))
	fmt.Fprintln(stdout, resp.Token)

	// Also print the RELAY_AGENT_ENROLLMENT_TOKEN= form for pasting into
	// operator docs / scripts.
	enc, _ := json.Marshal(resp.Token)
	_ = enc
	fmt.Fprintf(stderr, "set on agent host: RELAY_AGENT_ENROLLMENT_TOKEN=%s\n", resp.Token)
	return nil
}
```

**Notes on `stdout`/`stderr`**: the existing CLI package likely has `var stdout = os.Stdout; var stderr = os.Stderr` redirectable-for-tests globals. Check `grep -n 'var stdout\|var stderr' internal/cli/*.go`. If they don't exist, add them to a shared file (e.g. `internal/cli/io.go`) so tests can swap them.

**`newClient` / `client.PostJSON`**: these exist in `internal/cli/client.go`. Inspect it first to match the method signature — you may need to adapt the body-encoding style.

- [ ] **Step 11.3: Write the test**

`internal/cli/agent_enroll_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentEnroll_HappyPath(t *testing.T) {
	// Mock server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/agent-enrollments" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["hostname_hint"] != "render-07" {
			t.Fatalf("expected hostname_hint=render-07, got %v", body["hostname_hint"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"00000000-0000-0000-0000-000000000001","token":"raw-token-abc","expires_at":"2099-01-01T00:00:00Z"}`)
	}))
	defer srv.Close()

	cfg := &Config{Server: srv.URL, Token: "admin-token"}

	var outBuf, errBuf bytes.Buffer
	stdout = &outBuf
	stderr = &errBuf
	defer func() { stdout = nil; stderr = nil }()

	err := runAgentEnroll(context.Background(), []string{"--hostname", "render-07", "--ttl", "1h"}, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(outBuf.String(), "raw-token-abc") {
		t.Fatalf("stdout should contain raw token, got %q", outBuf.String())
	}
	if !strings.Contains(errBuf.String(), "enrollment expires at") {
		t.Fatalf("stderr should contain expiry, got %q", errBuf.String())
	}
	_ = time.Hour
}
```

Replace `stdout = &outBuf` with whatever the existing CLI tests use — inspect an adjacent `_test.go` for the pattern.

- [ ] **Step 11.4: Implement `internal/cli/workers_revoke.go`**

```go
package cli

import (
	"context"
	"flag"
	"fmt"
)

var WorkersRevoke = Command{
	Name:  "workers-revoke",
	Usage: "Revoke a worker's agent token (admin) — use <id-or-hostname>",
	Run:   runWorkersRevoke,
}

func runWorkersRevoke(ctx context.Context, args []string, cfg *Config) error {
	fs := flag.NewFlagSet("workers revoke", flag.ContinueOnError)
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: relay workers revoke <worker-id-or-hostname>")
	}
	target := fs.Arg(0)

	client, err := newClient(cfg)
	if err != nil {
		return err
	}

	id, err := resolveWorkerID(ctx, client, target)
	if err != nil {
		return err
	}

	if err := client.Delete(ctx, "/v1/workers/"+id+"/token"); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "revoked.")
	return nil
}

// resolveWorkerID returns a worker's UUID given either a UUID or hostname.
// Implemented by fetching /v1/workers and scanning; for plan scope keep it
// simple.
func resolveWorkerID(ctx context.Context, c *Client, target string) (string, error) {
	// If target parses as a UUID, pass through.
	if looksLikeUUID(target) {
		return target, nil
	}
	var list []struct {
		ID       string `json:"id"`
		Hostname string `json:"hostname"`
	}
	if err := c.GetJSON(ctx, "/v1/workers", &list); err != nil {
		return "", err
	}
	for _, w := range list {
		if w.Hostname == target {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("no worker with id or hostname %q", target)
}

func looksLikeUUID(s string) bool {
	// cheap heuristic: 8-4-4-4-12 hex
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}
```

**Note on `WorkersRevoke.Name = "workers-revoke"`**: the existing CLI's top-level command is a flat list (`relay login`, `relay submit`, etc.). Using `workers-revoke` as a single verb matches that style. If the CLI already has a `workers` group that takes subcommands (check `grep -n 'Name.*"workers"' internal/cli/*.go`), fold `revoke` in as a subcommand there instead.

**`client.Delete` and `client.GetJSON`**: may not exist with those exact names. Inspect `internal/cli/client.go` to match existing API; if a DELETE method doesn't exist, add one (small method alongside the existing GET/POST). Keep the addition to `client.go` focused.

- [ ] **Step 11.5: Write the revoke test**

`internal/cli/workers_revoke_test.go`:

```go
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkersRevoke_ByUUID(t *testing.T) {
	var deletedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	var outBuf bytes.Buffer
	stdout = &outBuf
	defer func() { stdout = nil }()

	err := runWorkersRevoke(context.Background(),
		[]string{"00000000-0000-0000-0000-000000000042"},
		&Config{Server: srv.URL, Token: "t"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if deletedPath != "/v1/workers/00000000-0000-0000-0000-000000000042/token" {
		t.Fatalf("wrong DELETE path: %s", deletedPath)
	}
	if !strings.Contains(outBuf.String(), "revoked.") {
		t.Fatalf("expected confirmation, got %q", outBuf.String())
	}
}

func TestWorkersRevoke_ByHostname(t *testing.T) {
	var deletedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/workers":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","hostname":"render-07"}]`)
		case r.Method == "DELETE":
			deletedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var outBuf bytes.Buffer
	stdout = &outBuf
	defer func() { stdout = nil }()

	err := runWorkersRevoke(context.Background(), []string{"render-07"}, &Config{Server: srv.URL, Token: "t"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if deletedPath != "/v1/workers/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa/token" {
		t.Fatalf("wrong DELETE path: %s", deletedPath)
	}
	_ = json.RawMessage{}
}
```

- [ ] **Step 11.6: Register both commands in `cmd/relay/main.go`**

Inspect `cmd/relay/main.go` to find the `cmds := []cli.Command{ ... }` list. Append:

```go
	cli.AgentEnroll,
	cli.WorkersRevoke,
```

- [ ] **Step 11.7: Run tests**

```bash
go test ./internal/cli/ -run "TestAgentEnroll|TestWorkersRevoke" -v
go build ./cmd/relay
```

Expected: tests pass; binary builds.

- [ ] **Step 11.8: Commit**

```bash
git add internal/cli/agent_enroll.go internal/cli/agent_enroll_test.go internal/cli/workers_revoke.go internal/cli/workers_revoke_test.go cmd/relay/main.go
# plus any client.go additions
git add internal/cli/client.go
git commit -m "feat(cli): add 'relay agent enroll' and 'relay workers revoke'"
```

---

## Task 12: Agent-side credential loading

**Files:**
- Create: `internal/agent/credentials.go`
- Create: `internal/agent/credentials_test.go`

- [ ] **Step 12.1: Write the failing tests**

`internal/agent/credentials_test.go`:

```go
package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadCredentials_EmptyWhenNoFile(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadCredentials(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c.HasAgentToken() {
		t.Fatalf("expected no agent token")
	}
}

func TestLoadCredentials_ReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("stored-token-abc\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCredentials(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !c.HasAgentToken() {
		t.Fatalf("expected HasAgentToken true")
	}
	if c.AgentToken() != "stored-token-abc" {
		t.Fatalf("got %q", c.AgentToken())
	}
}

func TestPersist_WritesWithRestrictivePerms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("0600 enforcement differs on Windows")
	}
	dir := t.TempDir()
	c, _ := LoadCredentials(dir)
	if err := c.Persist("new-token-xyz"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "token"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected 0600, got %o", info.Mode().Perm())
	}
	// Reload and verify.
	c2, err := LoadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c2.AgentToken() != "new-token-xyz" {
		t.Fatalf("got %q", c2.AgentToken())
	}
}

func TestSetEnrollmentToken(t *testing.T) {
	dir := t.TempDir()
	c, _ := LoadCredentials(dir)
	c.SetEnrollmentToken("enroll-1")
	if c.EnrollmentToken() != "enroll-1" {
		t.Fatal("enrollment token not set")
	}
	// After persisting an agent token, enrollment should be cleared.
	if err := c.Persist("agent-1"); err != nil {
		t.Fatal(err)
	}
	if c.EnrollmentToken() != "" {
		t.Fatalf("enrollment should be cleared after persist, got %q", c.EnrollmentToken())
	}
}
```

- [ ] **Step 12.2: Run to verify failure**

```bash
go test ./internal/agent/ -run "TestLoadCredentials|TestPersist|TestSetEnrollmentToken" -v
```

Expected: FAIL with "undefined" errors.

- [ ] **Step 12.3: Implement**

`internal/agent/credentials.go`:

```go
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Credentials manages the agent's authentication material. Two credentials
// exist:
//   - EnrollmentToken: a one-time bootstrap credential read from an env var,
//     sent only when no agent token has been persisted yet.
//   - AgentToken: a long-lived bearer persisted to <state-dir>/token after
//     the coordinator issues one in RegisterResponse.
type Credentials struct {
	tokenFilePath   string
	agentToken      string
	enrollmentToken string
}

// LoadCredentials reads the token file at <stateDir>/token if it exists.
// Missing file → empty credentials (no error). Corrupt permissions or unreadable
// file → error.
func LoadCredentials(stateDir string) (*Credentials, error) {
	path := filepath.Join(stateDir, "token")
	c := &Credentials{tokenFilePath: path}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, fmt.Errorf("read token file %s: %w", path, err)
	}
	c.agentToken = strings.TrimSpace(string(b))
	return c, nil
}

// HasAgentToken reports whether a persisted agent token is available.
func (c *Credentials) HasAgentToken() bool { return c.agentToken != "" }

// AgentToken returns the long-lived agent bearer token, or "" if none.
func (c *Credentials) AgentToken() string { return c.agentToken }

// EnrollmentToken returns the in-memory enrollment token, or "" if none.
func (c *Credentials) EnrollmentToken() string { return c.enrollmentToken }

// SetEnrollmentToken sets the in-memory enrollment token. Used once at agent
// startup, from the RELAY_AGENT_ENROLLMENT_TOKEN env var.
func (c *Credentials) SetEnrollmentToken(t string) { c.enrollmentToken = t }

// Persist writes the given agent token to the state file with 0600 perms and
// clears any in-memory enrollment token.
func (c *Credentials) Persist(agentToken string) error {
	if err := os.MkdirAll(filepath.Dir(c.tokenFilePath), 0755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	if err := os.WriteFile(c.tokenFilePath, []byte(agentToken), 0600); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	c.agentToken = agentToken
	c.enrollmentToken = ""
	return nil
}
```

- [ ] **Step 12.4: Run tests to pass**

```bash
go test ./internal/agent/ -run "TestLoadCredentials|TestPersist|TestSetEnrollmentToken" -v
```

Expected: all pass (Windows skips `TestPersist_WritesWithRestrictivePerms`).

- [ ] **Step 12.5: Commit**

```bash
git add internal/agent/credentials.go internal/agent/credentials_test.go
git commit -m "feat(agent): add credentials loader and token persistence"
```

---

## Task 13: Agent sends credential on Connect; handles revocation

**Files:**
- Modify: `internal/agent/agent.go` (constructor takes Credentials, buildRegisterRequest sets oneof, connect handles agent-token persistence, Unauthenticated drops to exit)
- Modify: `cmd/relay-agent/main.go` (loads credentials, reads env var if no file)
- Modify: existing `internal/agent/*_test.go` files that construct RegisterRequest or call `NewAgent` — add credential field.

**Breaking change warning**: after this task lands, agents without a credential file AND without the enrollment env var will fail to start. This task must merge together with Task 14; otherwise agents will be rejected by a server that doesn't yet enforce, which is harmless, but Task 14 merging alone would reject agents that never got Task 13. The combined rollout is correct.

- [ ] **Step 13.1: Extend `Agent` struct**

In `internal/agent/agent.go`, add a field to `Agent`:

```go
type Agent struct {
	// ... existing fields ...
	creds *Credentials
}
```

Change `NewAgent` signature to accept credentials:

```go
func NewAgent(coord string, caps Capabilities, workerID string, creds *Credentials, saveID func(string) error) *Agent {
	return &Agent{
		coord:    coord,
		caps:     caps,
		workerID: workerID,
		sendCh:   make(chan *relayv1.AgentMessage, 64),
		runners:  make(map[string]*Runner),
		saveID:   saveID,
		creds:    creds,
	}
}
```

- [ ] **Step 13.2: Update `buildRegisterRequest`**

Replace the function with:

```go
func (a *Agent) buildRegisterRequest() (*relayv1.RegisterRequest, error) {
	a.mu.Lock()
	running := make([]*relayv1.RunningTask, 0, len(a.runners))
	for _, r := range a.runners {
		running = append(running, &relayv1.RunningTask{
			TaskId: r.taskID,
			Epoch:  r.epoch,
		})
	}
	a.mu.Unlock()

	req := &relayv1.RegisterRequest{
		WorkerId:     a.workerID,
		Hostname:     a.caps.Hostname,
		CpuCores:     a.caps.CPUCores,
		RamGb:        a.caps.RAMGB,
		GpuCount:     a.caps.GPUCount,
		GpuModel:     a.caps.GPUModel,
		Os:           a.caps.OS,
		RunningTasks: running,
	}

	switch {
	case a.creds.HasAgentToken():
		req.Credential = &relayv1.RegisterRequest_AgentToken{AgentToken: a.creds.AgentToken()}
	case a.creds.EnrollmentToken() != "":
		req.Credential = &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: a.creds.EnrollmentToken()}
	default:
		return nil, fmt.Errorf("no credentials: set RELAY_AGENT_ENROLLMENT_TOKEN or provision the agent token file")
	}
	return req, nil
}
```

- [ ] **Step 13.3: Update the send-register block in `connect()`**

Replace the current block that sends `RegisterRequest` with:

```go
	regReq, err := a.buildRegisterRequest()
	if err != nil {
		return fmt.Errorf("build register: %w", err)
	}
	if err := stream.Send(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{Register: regReq},
	}); err != nil {
		return err
	}
```

- [ ] **Step 13.4: Handle agent_token from RegisterResponse**

After the existing `reg := resp.GetRegisterResponse()` check, before the `workerID` handling, add:

```go
	if reg.AgentToken != "" {
		if err := a.creds.Persist(reg.AgentToken); err != nil {
			return fmt.Errorf("persist agent token: %w", err)
		}
		log.Printf("agent token persisted to %s", a.creds.tokenFilePath)
	}
```

Note: `a.creds.tokenFilePath` is currently unexported; expose it via a method `TokenFilePath() string` on `Credentials`, or log without the path. Pick one and stay consistent.

- [ ] **Step 13.5: Detect Unauthenticated and exit the Run loop**

In the existing `Run` method's reconnect loop, distinguish unauth errors. Modify the err branch:

```go
		if err := a.connect(ctx); err != nil {
			if status.Code(err) == codes.Unauthenticated {
				log.Printf("agent: authentication failed — token may have been revoked; exiting")
				a.runnerWG.Wait()
				return
			}
			// ... existing backoff logic ...
		}
```

Add imports:

```go
import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)
```

- [ ] **Step 13.6: Update `cmd/relay-agent/main.go`**

Replace the state-load block. After `workerID := loadWorkerID(workerIDFile)`, add:

```go
	creds, err := agent.LoadCredentials(*stateDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "relay-agent: load credentials: %v\n", err)
		os.Exit(1)
	}
	if !creds.HasAgentToken() {
		if t := os.Getenv("RELAY_AGENT_ENROLLMENT_TOKEN"); t != "" {
			creds.SetEnrollmentToken(t)
		} else {
			fmt.Fprintf(os.Stderr, "relay-agent: no credentials available — set RELAY_AGENT_ENROLLMENT_TOKEN for first boot, or provision the agent token file\n")
			os.Exit(1)
		}
	}
```

Update the `agent.NewAgent` call:

```go
	a := agent.NewAgent(addr, caps, workerID, creds, func(id string) error {
		return saveWorkerID(workerIDFile, id)
	})
```

- [ ] **Step 13.7: Update existing agent tests**

Run: `grep -rn "NewAgent(" internal/agent/ cmd/relay-agent/`. Every call site needs an updated `*Credentials` argument. In test-only code use a helper:

```go
func testCreds(t *testing.T) *agent.Credentials {
	c, err := agent.LoadCredentials(t.TempDir())
	if err != nil { t.Fatal(err) }
	c.SetEnrollmentToken("test-enrollment") // or pre-persist an agent token
	return c
}
```

If a test exercises buildRegisterRequest directly (epoch test, for example), update it to expect a non-nil credential on the resulting message. Inspect `internal/agent/agent_test.go` tests named around `TestRunnerTagsOutgoingMessagesWithEpoch`, `TestAgent_dispatchAndReceiveLogs`, and any `TestBuildRegisterRequest` — update their setup and add an assertion on `req.Credential` being populated.

- [ ] **Step 13.8: Run all agent tests and build**

```bash
go build ./...
go test ./internal/agent/ -v -race -timeout 60s
go test ./cmd/relay-agent/ -v
```

Expected: all pass.

- [ ] **Step 13.9: Update `CLAUDE.md`**

Add to the env var table:

```markdown
| `RELAY_AGENT_ENROLLMENT_TOKEN` | — | One-time enrollment credential for a fresh agent host. Read only when `<state-dir>/token` does not exist. |
```

Also add a brief note in the "relay-agent internals" section about the token file at `<state-dir>/token`.

- [ ] **Step 13.10: Commit**

```bash
git add internal/agent/agent.go cmd/relay-agent/main.go CLAUDE.md
git add internal/agent/  # test updates
git commit -m "feat(agent): send credential on Connect; persist agent token from server"
```

---

## Task 14: Server-side Connect auth enforcement

**Files:**
- Modify: `internal/worker/handler.go` (auth branch in `Connect`, new `authenticateAndRegister`)
- Modify: `internal/worker/handler_test.go` (update all existing RegisterRequest construction, add new auth tests)
- Create: `internal/worker/handler_auth_test.go` (new auth-specific integration tests)

**This is the breaking change point.** After merge, all pre-existing workers must re-enroll.

- [ ] **Step 14.1: Write failing auth tests**

`internal/worker/handler_auth_test.go`:

```go
//go:build integration

package worker_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"relay/internal/events"
	relayv1 "relay/internal/proto/relayv1"
	"relay/internal/store"
	"relay/internal/worker"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// seedEnrollment creates an enrollment directly in the DB and returns the raw
// token + hash. Admin user must exist in the fixture.
func seedEnrollment(t *testing.T, ctx context.Context, q *store.Queries, adminID pgtype.UUID, ttl time.Duration) (rawToken string, hash string) {
	t.Helper()
	raw := "enroll-" + t.Name()
	sum := sha256.Sum256([]byte(raw))
	h := hex.EncodeToString(sum[:])
	_, err := q.CreateAgentEnrollment(ctx, store.CreateAgentEnrollmentParams{
		TokenHash: h,
		CreatedBy: adminID,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(ttl), Valid: true},
	})
	require.NoError(t, err)
	return raw, h
}

func TestConnect_ValidEnrollmentIssuesAgentToken(t *testing.T) {
	ctx := context.Background()
	fx := newWorkerTestFixture(t)  // creates q, broker, registry, admin user, handler
	defer fx.Cleanup()

	rawToken, _ := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)

	stream := newMockConnectStream(t)
	defer stream.Close()

	go func() {
		_ = fx.Handler.Connect(stream)
	}()

	stream.SendToServer(&relayv1.AgentMessage{
		Payload: &relayv1.AgentMessage_Register{
			Register: &relayv1.RegisterRequest{
				Hostname:   "testhost",
				CpuCores:   4,
				RamGb:      16,
				Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawToken},
			},
		},
	})

	resp := stream.RecvFromServer(t, 2*time.Second)
	reg := resp.GetRegisterResponse()
	require.NotNil(t, reg)
	require.NotEmpty(t, reg.AgentToken, "server should issue agent token on first enrollment")
	require.NotEmpty(t, reg.WorkerId)

	// Close the stream to end Connect.
	stream.CloseSend()
}

func TestConnect_AgentTokenAuthSucceeds(t *testing.T) {
	ctx := context.Background()
	fx := newWorkerTestFixture(t)
	defer fx.Cleanup()

	rawToken, _ := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)

	// First connect: enroll, capture agent_token.
	stream1 := newMockConnectStream(t)
	done1 := make(chan error, 1)
	go func() { done1 <- fx.Handler.Connect(stream1) }()

	stream1.SendToServer(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_Register{Register: &relayv1.RegisterRequest{
		Hostname:   "host2",
		Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawToken},
	}}})
	resp := stream1.RecvFromServer(t, 2*time.Second)
	require.NotEmpty(t, resp.GetRegisterResponse().AgentToken)
	agentToken := resp.GetRegisterResponse().AgentToken
	stream1.CloseSend()
	<-done1

	// Second connect with the agent token.
	stream2 := newMockConnectStream(t)
	done2 := make(chan error, 1)
	go func() { done2 <- fx.Handler.Connect(stream2) }()

	stream2.SendToServer(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_Register{Register: &relayv1.RegisterRequest{
		Hostname:   "host2",
		Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: agentToken},
	}}})
	resp2 := stream2.RecvFromServer(t, 2*time.Second)
	require.NotNil(t, resp2.GetRegisterResponse())
	stream2.CloseSend()
	<-done2
}

func TestConnect_RevokedTokenRejected(t *testing.T) {
	ctx := context.Background()
	fx := newWorkerTestFixture(t)
	defer fx.Cleanup()

	rawToken, _ := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)

	// Enroll once.
	s1 := newMockConnectStream(t)
	d1 := make(chan error, 1)
	go func() { d1 <- fx.Handler.Connect(s1) }()
	s1.SendToServer(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_Register{Register: &relayv1.RegisterRequest{
		Hostname:   "host3",
		Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawToken},
	}}})
	resp := s1.RecvFromServer(t, 2*time.Second)
	agentToken := resp.GetRegisterResponse().AgentToken
	workerID := resp.GetRegisterResponse().WorkerId
	s1.CloseSend()
	<-d1

	// Revoke.
	var wID pgtype.UUID
	require.NoError(t, wID.Scan(workerID))
	require.NoError(t, fx.Q.ClearWorkerAgentToken(ctx, wID))

	// Reconnect with the revoked token.
	s2 := newMockConnectStream(t)
	d2 := make(chan error, 1)
	go func() { d2 <- fx.Handler.Connect(s2) }()
	s2.SendToServer(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_Register{Register: &relayv1.RegisterRequest{
		Hostname:   "host3",
		Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: agentToken},
	}}})

	err := <-d2
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestConnect_EnrollmentTokenSingleShot(t *testing.T) {
	ctx := context.Background()
	fx := newWorkerTestFixture(t)
	defer fx.Cleanup()

	rawToken, _ := seedEnrollment(t, ctx, fx.Q, fx.AdminID, time.Hour)

	// First use — succeeds.
	s1 := newMockConnectStream(t)
	d1 := make(chan error, 1)
	go func() { d1 <- fx.Handler.Connect(s1) }()
	s1.SendToServer(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_Register{Register: &relayv1.RegisterRequest{
		Hostname:   "host4",
		Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawToken},
	}}})
	s1.RecvFromServer(t, 2*time.Second)
	s1.CloseSend()
	<-d1

	// Second use — should be Unauthenticated.
	s2 := newMockConnectStream(t)
	d2 := make(chan error, 1)
	go func() { d2 <- fx.Handler.Connect(s2) }()
	s2.SendToServer(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_Register{Register: &relayv1.RegisterRequest{
		Hostname:   "host4b",
		Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawToken},
	}}})
	err := <-d2
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestConnect_ExpiredEnrollmentRejected(t *testing.T) {
	ctx := context.Background()
	fx := newWorkerTestFixture(t)
	defer fx.Cleanup()

	rawToken, _ := seedEnrollment(t, ctx, fx.Q, fx.AdminID, -time.Hour) // already expired

	s := newMockConnectStream(t)
	d := make(chan error, 1)
	go func() { d <- fx.Handler.Connect(s) }()
	s.SendToServer(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_Register{Register: &relayv1.RegisterRequest{
		Hostname:   "host5",
		Credential: &relayv1.RegisterRequest_EnrollmentToken{EnrollmentToken: rawToken},
	}}})
	err := <-d
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestConnect_NoCredentialRejected(t *testing.T) {
	fx := newWorkerTestFixture(t)
	defer fx.Cleanup()

	s := newMockConnectStream(t)
	d := make(chan error, 1)
	go func() { d <- fx.Handler.Connect(s) }()
	s.SendToServer(&relayv1.AgentMessage{Payload: &relayv1.AgentMessage_Register{Register: &relayv1.RegisterRequest{
		Hostname: "host6",
	}}})
	err := <-d
	require.Error(t, err)
	require.Equal(t, codes.Unauthenticated, status.Code(err))

	_ = io.EOF
	_ = events.Event{}
	_ = worker.NewRegistry
}
```

**Test fixture notes**: `newWorkerTestFixture` and `newMockConnectStream` are helpers. `newWorkerTestFixture` likely exists already — inspect `internal/worker/handler_test.go` for a similar helper pattern. If it doesn't expose `AdminID`, extend it. `newMockConnectStream` should already exist — it's used by the existing `handler_test.go` tests for Connect, SendToServer, RecvFromServer, CloseSend. Reuse whatever pattern is there.

- [ ] **Step 14.2: Run to verify failure**

```bash
go test -tags integration -p 1 ./internal/worker/ -run "TestConnect_" -v -timeout 180s
```

Expected: FAIL (Connect doesn't validate credentials yet).

- [ ] **Step 14.3: Implement auth in `internal/worker/handler.go`**

Add a new method `authenticateAndRegister` and rewire `Connect`:

```go
import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	// existing imports...
)
```

Replace the body of `Connect` up to and including `registerWorker`. The full new flow:

```go
// Connect implements relayv1.AgentServiceServer.
func (h *Handler) Connect(stream relayv1.AgentService_ConnectServer) error {
	ctx := stream.Context()

	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("recv first message: %w", err)
	}
	reg := first.GetRegister()
	if reg == nil {
		return fmt.Errorf("first message must be RegisterRequest")
	}

	// Authenticate and register atomically.
	workerID, sender, err := h.authenticateAndRegister(ctx, stream, reg)
	if err != nil {
		return err
	}

	if h.grace != nil {
		defer h.grace.Start(workerID)
	} else {
		defer h.requeueWorkerTasks(workerID)
	}
	defer h.markWorkerOffline(workerID)
	defer sender.Close()
	defer h.registry.Unregister(workerID)

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch p := msg.Payload.(type) {
		case *relayv1.AgentMessage_TaskStatus:
			h.handleTaskStatus(ctx, p.TaskStatus)
		case *relayv1.AgentMessage_TaskLog:
			h.handleTaskLog(ctx, p.TaskLog)
		}
	}
}

// authenticateAndRegister validates reg.credential, runs the existing register
// flow, and (on enrollment) issues a fresh agent token. Returns gRPC
// Unauthenticated for any auth failure (no enumeration).
func (h *Handler) authenticateAndRegister(
	ctx context.Context,
	stream relayv1.AgentService_ConnectServer,
	reg *relayv1.RegisterRequest,
) (string, *workerSender, error) {

	switch cred := reg.Credential.(type) {
	case *relayv1.RegisterRequest_EnrollmentToken:
		return h.enrollAndRegister(ctx, stream, reg, cred.EnrollmentToken)
	case *relayv1.RegisterRequest_AgentToken:
		return h.reconnectAndRegister(ctx, stream, reg, cred.AgentToken)
	default:
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
}

func (h *Handler) enrollAndRegister(
	ctx context.Context,
	stream relayv1.AgentService_ConnectServer,
	reg *relayv1.RegisterRequest,
	rawEnroll string,
) (string, *workerSender, error) {

	if rawEnroll == "" {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}

	sum := sha256.Sum256([]byte(rawEnroll))
	hash := hex.EncodeToString(sum[:])
	enroll, err := h.q.GetAgentEnrollmentByTokenHash(ctx, hash)
	if err != nil {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
	if enroll.ConsumedAt.Valid {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
	if !enroll.ExpiresAt.Valid || time.Now().After(enroll.ExpiresAt.Time) {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}

	// Generate fresh agent token.
	rawAgentBytes := make([]byte, 32)
	if _, err := cryptorand.Read(rawAgentBytes); err != nil {
		return "", nil, status.Errorf(codes.Internal, "token gen failed")
	}
	rawAgent := hex.EncodeToString(rawAgentBytes)
	sumAgent := sha256.Sum256([]byte(rawAgent))
	agentHash := hex.EncodeToString(sumAgent[:])

	// Run the shared register flow and persist the new token atomically.
	workerID, sender, err := h.registerWorkerWithToken(ctx, stream, reg, agentHash, rawAgent)
	if err != nil {
		return "", nil, err
	}

	// Consume enrollment AFTER we know registration + token persistence succeeded.
	var workerUUID pgtype.UUID
	_ = workerUUID.Scan(workerID)
	rows, err := h.q.ConsumeAgentEnrollment(ctx, store.ConsumeAgentEnrollmentParams{
		ID:         enroll.ID,
		ConsumedBy: workerUUID,
	})
	if err != nil {
		return "", nil, status.Errorf(codes.Internal, "consume enrollment failed: %v", err)
	}
	if rows == 0 {
		// Racing enrollment consumption. Another Connect claimed the same token.
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}

	return workerID, sender, nil
}

func (h *Handler) reconnectAndRegister(
	ctx context.Context,
	stream relayv1.AgentService_ConnectServer,
	reg *relayv1.RegisterRequest,
	rawAgent string,
) (string, *workerSender, error) {

	if rawAgent == "" {
		return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
	}
	sum := sha256.Sum256([]byte(rawAgent))
	hash := hex.EncodeToString(sum[:])

	w, err := h.q.GetWorkerByAgentTokenHash(ctx, &hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, status.Errorf(codes.Unauthenticated, "authentication failed")
		}
		return "", nil, status.Errorf(codes.Internal, "token lookup failed")
	}

	// Sanity: hostname in the request should match the stored worker. Mismatch
	// is suspicious but not blocking — we log and proceed with the stored row.
	if reg.Hostname != "" && reg.Hostname != w.Hostname {
		// Defensive log; not auth-fatal.
	}

	// Proceed with the existing register flow, skipping the token-issuance step.
	return h.registerWorkerExisting(ctx, stream, reg, w)
}
```

Now refactor the existing `registerWorker` into two helpers: `registerWorkerWithToken` (for enrollments — sets the hash as part of registration) and `registerWorkerExisting` (for reconnects — worker row already known). Both share the body of what is currently `registerWorker`. Show both explicitly:

```go
// registerWorkerWithToken is the enrollment register path: upsert the worker,
// store the fresh agent token hash, reconcile, and send RegisterResponse
// (including the raw agent_token).
func (h *Handler) registerWorkerWithToken(
	ctx context.Context,
	stream relayv1.AgentService_ConnectServer,
	reg *relayv1.RegisterRequest,
	agentTokenHash string,
	rawAgentToken string,
) (string, *workerSender, error) {

	w, err := h.q.UpsertWorkerByHostname(ctx, store.UpsertWorkerByHostnameParams{
		Name:     reg.Hostname,
		Hostname: reg.Hostname,
		CpuCores: reg.CpuCores,
		RamGb:    reg.RamGb,
		GpuCount: reg.GpuCount,
		GpuModel: reg.GpuModel,
		Os:       reg.Os,
	})
	if err != nil {
		return "", nil, fmt.Errorf("upsert worker: %w", err)
	}

	if err := h.q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{
		ID:             w.ID,
		AgentTokenHash: &agentTokenHash,
	}); err != nil {
		return "", nil, fmt.Errorf("set agent token: %w", err)
	}

	return h.finishRegister(ctx, stream, reg, w, rawAgentToken)
}

// registerWorkerExisting is the reconnect register path: worker row is already
// resolved by token hash, just mark online and reconcile.
func (h *Handler) registerWorkerExisting(
	ctx context.Context,
	stream relayv1.AgentService_ConnectServer,
	reg *relayv1.RegisterRequest,
	w store.Worker,
) (string, *workerSender, error) {
	return h.finishRegister(ctx, stream, reg, w, "")
}

// finishRegister is the shared tail of both register paths.
// rawAgentToken is "" for reconnects (no token issued) and the raw token for
// fresh enrollments (to include in RegisterResponse.agent_token).
func (h *Handler) finishRegister(
	ctx context.Context,
	stream relayv1.AgentService_ConnectServer,
	reg *relayv1.RegisterRequest,
	w store.Worker,
	rawAgentToken string,
) (string, *workerSender, error) {

	w, err := h.q.UpdateWorkerStatus(ctx, store.UpdateWorkerStatusParams{
		ID:         w.ID,
		Status:     "online",
		LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		return "", nil, fmt.Errorf("update worker status: %w", err)
	}

	workerID := uuidStr(w.ID)

	if h.grace != nil {
		h.grace.Cancel(workerID)
	}

	cancelIDs, err := h.reconcileRunningTasks(ctx, w.ID, reg.RunningTasks)
	if err != nil {
		return "", nil, fmt.Errorf("reconcile: %w", err)
	}

	if err := stream.Send(&relayv1.CoordinatorMessage{
		Payload: &relayv1.CoordinatorMessage_RegisterResponse{
			RegisterResponse: &relayv1.RegisterResponse{
				WorkerId:      workerID,
				CancelTaskIds: cancelIDs,
				AgentToken:    rawAgentToken,
			},
		},
	}); err != nil {
		return "", nil, fmt.Errorf("send register response: %w", err)
	}

	sender := NewWorkerSender(stream)
	h.registry.Register(workerID, sender)

	h.broker.Publish(events.Event{
		// whatever the existing registerWorker publishes — copy verbatim from
		// the previous implementation. Do not drop any field.
	})

	h.triggerDispatch()
	return workerID, sender, nil
}
```

**Crucial**: the event publish block at the end of the old `registerWorker` must be preserved exactly. Open the original `registerWorker` and copy its `h.broker.Publish(events.Event{...})` call verbatim into `finishRegister`. Don't trust this plan's placeholder — use the real one.

Also add the imports for the new helpers:

```go
import (
	cryptorand "crypto/rand"
	// ...
)
```

Delete the old `registerWorker` once `finishRegister` is in place and all callers use the new helpers.

- [ ] **Step 14.4: Update existing `handler_test.go` fixtures**

Every existing test that sends a RegisterRequest must now include a credential. Either:

- Seed an enrollment in the test setup and pass `EnrollmentToken`, OR
- Pre-populate `workers.agent_token_hash` and pass `AgentToken`.

The second is usually simpler for existing tests. Add a helper near the top of `internal/worker/handler_test.go`:

```go
func seedWorkerWithAgentToken(t *testing.T, ctx context.Context, q *store.Queries, hostname string) (workerID pgtype.UUID, rawToken string) {
	t.Helper()
	w, err := q.CreateWorker(ctx, store.CreateWorkerParams{
		Name: hostname, Hostname: hostname, CpuCores: 1, RamGb: 1, GpuCount: 0, Os: "linux",
	})
	require.NoError(t, err)
	raw := "test-agent-token-" + hostname
	sum := sha256.Sum256([]byte(raw))
	h := hex.EncodeToString(sum[:])
	require.NoError(t, q.SetWorkerAgentToken(ctx, store.SetWorkerAgentTokenParams{ID: w.ID, AgentTokenHash: &h}))
	return w.ID, raw
}
```

Search for every call to `newMockConnectStream` or any site that sends a `RegisterRequest` and attach `Credential: &relayv1.RegisterRequest_AgentToken{AgentToken: rawToken}`. This will touch multiple tests; handle one at a time.

- [ ] **Step 14.5: Run all worker tests**

```bash
go build ./...
go test ./internal/worker/ -v -race -timeout 60s
go test -tags integration -p 1 ./internal/worker/ -v -timeout 300s
```

Expected: all pass, new auth tests pass, no regressions in existing reconcile/grace tests.

- [ ] **Step 14.6: Commit**

```bash
git add internal/worker/handler.go internal/worker/handler_test.go internal/worker/handler_auth_test.go
git commit -m "feat(worker): enforce credential auth on Connect"
```

---

## Task 15: Janitorial ticker for expired enrollments

**Files:**
- Modify: `cmd/relay-server/main.go` (add goroutine)

- [ ] **Step 15.1: Add the ticker**

In `cmd/relay-server/main.go`, after `go dispatcher.Run(ctx)` and before HTTP startup, add:

```go
	// Prune expired enrollment tokens every hour.
	go runEnrollmentJanitor(ctx, q)
```

Add the function at the bottom of the file:

```go
// runEnrollmentJanitor deletes expired unconsumed agent enrollments every hour.
// Errors are logged and retried on the next tick; the janitor never exits the
// server process.
func runEnrollmentJanitor(ctx context.Context, q *store.Queries) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := q.DeleteExpiredAgentEnrollments(ctx); err != nil {
				log.Printf("enrollment janitor: %v", err)
			}
		}
	}
}
```

- [ ] **Step 15.2: Build and verify**

```bash
go build ./cmd/relay-server/
```

Expected: builds cleanly.

- [ ] **Step 15.3: Commit**

```bash
git add cmd/relay-server/main.go
git commit -m "feat(server): add hourly janitor for expired agent enrollments"
```

---

## Final Verification

After Task 15 commits, before opening the PR:

- [ ] Run the complete suite:

```bash
go build ./...
go test ./... -race -timeout 120s
go test -tags integration -p 1 ./... -timeout 600s
```

- [ ] Manual smoke test the breaking change rollout on a dev instance:

```bash
# 1. Start server with a bootstrap admin.
RELAY_BOOTSTRAP_ADMIN=admin@example.com RELAY_BOOTSTRAP_PASSWORD=devpass ./bin/relay-server

# 2. In another shell, log in and mint an enrollment token.
./bin/relay login
./bin/relay agent enroll --hostname test-host --ttl 1h
# → copy the token

# 3. Start an agent with the enrollment token.
RELAY_AGENT_ENROLLMENT_TOKEN=<paste> ./bin/relay-agent --coordinator localhost:9090

# 4. Agent should connect, log "agent token persisted", continue running.

# 5. Stop and restart the agent WITHOUT the env var.
./bin/relay-agent --coordinator localhost:9090
# → should reconnect using the persisted token.

# 6. Revoke the agent.
./bin/relay workers revoke test-host

# 7. Agent should log "authentication failed — token may have been revoked; exiting" on its next reconnect.

# 8. Re-enroll.
./bin/relay agent enroll --hostname test-host --ttl 1h
rm -rf /var/lib/relay-agent/token    # (or equivalent on Windows)
RELAY_AGENT_ENROLLMENT_TOKEN=<paste> ./bin/relay-agent --coordinator localhost:9090
# → should re-enroll and flip worker status back to online.
```

- [ ] Confirm `RELAY_BOOTSTRAP_PASSWORD` is absent after startup:

```bash
# With server running in foreground shell:
# In a separate shell:
PID=$(pgrep -f relay-server)
strings /proc/$PID/environ | grep BOOTSTRAP  # Linux
# Expected: no output.
```

- [ ] Confirm rate limiting fires:

```bash
for i in {1..15}; do
  curl -s -o /dev/null -w "%{http_code}\n" \
    -X POST http://localhost:8080/v1/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"email":"nobody@example.com","password":"x"}'
done
# Expected: first 10 return 401, subsequent 5 return 429.
```

- [ ] Confirm CORS empty default:

```bash
curl -s -i -X OPTIONS http://localhost:8080/v1/jobs \
  -H 'Origin: https://example.com' \
  -H 'Access-Control-Request-Method: POST'
# Expected: 204 with no Access-Control-Allow-Origin header.
```

- [ ] Write release notes covering the breaking Task-14 change:
  - All existing agents must be re-enrolled after upgrade
  - Operators must mint enrollment tokens via `relay agent enroll`
  - Document the new env vars: `RELAY_CORS_ORIGINS`, `RELAY_LOGIN_RATE_LIMIT`, `RELAY_REGISTER_RATE_LIMIT`, `RELAY_AGENT_ENROLLMENT_TOKEN`

---

## Spec coverage check

| Spec section | Covered by |
|---|---|
| §1 Agent enrollment data model | Tasks 6, 7, 8 |
| §1 Proto additions | Task 9 |
| §1 Server-side Connect flow | Task 14 |
| §1 Admin HTTP endpoints | Task 10 |
| §1 Agent-side credential handling | Tasks 12, 13 |
| §1 Admin CLI | Task 11 |
| §1 Rollout break | Task 14 commit message + final verification section |
| §2 Rate limiting | Tasks 3, 4 |
| §3 CORS policy | Tasks 1, 2 |
| §4 Bootstrap password lifetime | Task 5 |
| §5 Janitorial ticker | Task 15 |
| §Residual risks | Documented in CLAUDE.md / spec; no code |
| §Configuration reference | CLAUDE.md updates in Tasks 2, 4, 5, 13 |
