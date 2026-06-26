package mcp

import (
	"os"
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
