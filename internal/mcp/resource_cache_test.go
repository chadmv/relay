package mcp

import (
	"context"
	"sync/atomic"
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

func TestRecentJobsCache_HitWithinTTL(t *testing.T) {
	var calls int32
	fetch := func(ctx context.Context) ([]byte, *ToolError) {
		atomic.AddInt32(&calls, 1)
		return []byte(`{"items":[],"total":0}`), nil
	}
	c := &recentJobsCache{ttl: time.Minute, now: time.Now}

	b1, terr := c.get(context.Background(), fetch)
	require.Nil(t, terr)
	require.Equal(t, `{"items":[],"total":0}`, string(b1))

	b2, terr := c.get(context.Background(), fetch)
	require.Nil(t, terr)
	require.Equal(t, `{"items":[],"total":0}`, string(b2))

	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "second read should be served from cache")
}

func TestRecentJobsCache_ExpiryRefetches(t *testing.T) {
	var calls int32
	fetch := func(ctx context.Context) ([]byte, *ToolError) {
		atomic.AddInt32(&calls, 1)
		return []byte(`{"items":[],"total":0}`), nil
	}

	var fake time.Time = time.Unix(0, 0)
	clock := func() time.Time { return fake }
	c := &recentJobsCache{ttl: 10 * time.Second, now: clock}

	_, terr := c.get(context.Background(), fetch)
	require.Nil(t, terr)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))

	// Within TTL: still one call.
	fake = fake.Add(5 * time.Second)
	_, terr = c.get(context.Background(), fetch)
	require.Nil(t, terr)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))

	// Past TTL: refetch.
	fake = fake.Add(10 * time.Second)
	_, terr = c.get(context.Background(), fetch)
	require.Nil(t, terr)
	require.Equal(t, int32(2), atomic.LoadInt32(&calls))
}
