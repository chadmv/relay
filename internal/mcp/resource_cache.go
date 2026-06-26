package mcp

import (
	"context"
	"os"
	"sync"
	"time"
)

// defaultResourceCacheTTL is the recent-jobs cache window when
// RELAY_MCP_RESOURCE_CACHE_TTL is unset or unparseable.
const defaultResourceCacheTTL = 10 * time.Second

// resourceCacheTTL is the effective recent-jobs cache TTL, resolved once from
// RELAY_MCP_RESOURCE_CACHE_TTL (a Go duration, e.g. 10s, 30s, 0). A value of 0
// (or negative) disables caching. It is a package var (not a const) so tests can
// override it; this mirrors the RELAY_EVICTION_TIMEOUT / evictTimeout convention.
var resourceCacheTTL = resolveResourceCacheTTL()

// resolveResourceCacheTTL reads RELAY_MCP_RESOURCE_CACHE_TTL and falls back to
// defaultResourceCacheTTL on unset/unparseable input. A parsed value of 0 or a
// negative duration is honored as "caching disabled" (returned as-is).
func resolveResourceCacheTTL() time.Duration {
	v := os.Getenv("RELAY_MCP_RESOURCE_CACHE_TTL")
	if v == "" {
		return defaultResourceCacheTTL
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultResourceCacheTTL
	}
	return d
}

// recentJobsCache is a single-slot TTL cache for the relay://recent-jobs resource.
// The resource takes no parameters, so one cached value with a timestamp suffices.
// All access is guarded by mu; get holds the lock across a miss's fetch, which
// single-flights concurrent misses (acceptable for a single-process stdio server
// with a fast backend).
type recentJobsCache struct {
	mu        sync.Mutex
	body      []byte           // last successful marshaled JSON; nil => no value yet
	fetchedAt time.Time        // zero => no value yet
	ttl       time.Duration    // <=0 => caching disabled
	now       func() time.Time // injectable clock; defaults to time.Now
}

// get returns the cached recent-jobs JSON, refetching via fetch when the value is
// absent or stale. TTL<=0 disables caching: it always fetches and stores nothing.
// On a fetch error the previous (stale) value is left intact and the error is
// returned. The returned slice is always a copy; the caller never receives the
// cache's backing array.
func (c *recentJobsCache) get(ctx context.Context, fetch func(context.Context) ([]byte, *ToolError)) ([]byte, *ToolError) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ttl <= 0 {
		return fetch(ctx)
	}

	if c.body != nil && c.now().Sub(c.fetchedAt) < c.ttl {
		return cloneBytes(c.body), nil
	}

	body, terr := fetch(ctx)
	if terr != nil {
		return nil, terr
	}
	c.body = body
	c.fetchedAt = c.now()
	return cloneBytes(c.body), nil
}

func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
