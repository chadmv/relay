package mcp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResolveResourceCacheTTL(t *testing.T) {
	t.Setenv("RELAY_MCP_RESOURCE_CACHE_TTL", "")
	require.Equal(t, defaultResourceCacheTTL, resolveResourceCacheTTL())

	t.Setenv("RELAY_MCP_RESOURCE_CACHE_TTL", "30s")
	require.Equal(t, 30*time.Second, resolveResourceCacheTTL())

	t.Setenv("RELAY_MCP_RESOURCE_CACHE_TTL", "0")
	require.Equal(t, time.Duration(0), resolveResourceCacheTTL())

	t.Setenv("RELAY_MCP_RESOURCE_CACHE_TTL", "not-a-duration")
	require.Equal(t, defaultResourceCacheTTL, resolveResourceCacheTTL())
}
