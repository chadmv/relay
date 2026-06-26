# MCP Resource Templates and Recent-Jobs TTL Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `relay://jobs/{id}` and `relay://tasks/{id}` resource templates to the relay MCP server, and put a configurable TTL cache in front of `relay://recent-jobs`.

**Architecture:** Two parts, both entirely in `internal/mcp` (no relay-server / API change). Part 1 registers two URI-templated resources via `(*mcpsdk.Server).AddResourceTemplate`; each handler extracts the id from `req.Params.URI`, GETs the single-entity endpoint through the existing `s.do` chokepoint, and returns the JSON as a `ReadResourceResult`. Part 2 adds a `recentJobsCache` value on `Server` (sync.Mutex, cached bytes + fetchedAt + injectable `now` clock + resolved TTL) and routes the `recent-jobs` handler through it. TTL is resolved once from `RELAY_MCP_RESOURCE_CACHE_TTL` at construction, mirroring the `RELAY_EVICTION_TIMEOUT` pattern.

**Tech Stack:** Go, `github.com/modelcontextprotocol/go-sdk v1.6.0`, `net/http/httptest`, `testify/require`.

---

## Slice independence

**Backend-only.** This plan touches only `internal/mcp` (Go). There is no frontend slice and no relay-server change. Part 1 (templates) and Part 2 (cache) are independent of each other and may be implemented in parallel, but the plan orders Part 1 first then Part 2 for a single sequential worker. There is no `//go:build` gate: every test in this plan runs on Windows and under Docker (`-race` for the concurrency test).

## Invariants check

This change lives in the MCP client/resource layer. It performs no `tasks.status` / `task_logs` write, no gRPC stream send, no store call, and reads request bodies through no new path. The server-side Invariants (epoch fence, single job-spec pipeline, one bounded sender per stream, identity-checked teardown, single JSON entry point) are not in play. The one local rule that applies is **no interior pointers across locks**: `recentJobsCache.get` must return a COPY of its cached `[]byte`, never the backing slice (Task 8). No `.sql` / `.proto` edits, so no `make generate`.

## Grounded API confirmations (cited, do not re-verify)

- Template registration: `func (s *Server) AddResourceTemplate(t *ResourceTemplate, h ResourceHandler)` at `C:\Users\chadv\go\pkg\mod\github.com\modelcontextprotocol\go-sdk@v1.6.0\mcp\server.go:538`. It validates the template via `uritemplate.New(t.URITemplate)` and panics on an invalid template.
- `type ResourceTemplate struct` at `...\go-sdk@v1.6.0\mcp\protocol.go:1152` has fields `Name`, `Title`, `Description`, `MIMEType`, `URITemplate` (plus `Meta`, `Annotations`, `Icons`). `URITemplate` is RFC 6570.
- `type ResourceHandler func(context.Context, *ReadResourceRequest) (*ReadResourceResult, error)` at `...\go-sdk@v1.6.0\mcp\resource.go:38` — same signature already used by `AddResource`.
- `func ResourceNotFoundError(uri string) error` at `...\go-sdk@v1.6.0\mcp\resource.go:42` returns a `*jsonrpc.Error` with code `CodeResourceNotFound` and `Data` = `{"uri":<uri>}`.
- Routing: `readResource` at `...\go-sdk@v1.6.0\mcp\server.go:791` reads `uri := req.Params.URI`, calls `lookupResourceHandler` (server.go:822) which tries exact resources then iterates `s.resourceTemplates.all()` calling `rt.Matches(uri)`. The matched handler is invoked with the concrete URI in `req.Params.URI`; **the SDK does NOT pass extracted template variables** — the handler must parse the id from `req.Params.URI` itself.
- Backend endpoints exist (no API change): `GET /v1/jobs/{id}` -> `handleGetJob` at `internal/api/server.go:118`; `GET /v1/tasks/{id}` -> `handleGetTask` at `internal/api/server.go:123`.
- Chokepoint: `(*Server).do(ctx, method, path, body, out)` at `internal/mcp/do.go:16`. A 404 surfaces as a `*relayclient.ResponseError` with `StatusCode == 404`; `MapError(err).Code == "not_found"` (`internal/mcp/errors.go:33`).
- Env-duration precedent: `resolveEvictTimeout()` reading `RELAY_EVICTION_TIMEOUT` at `internal/agent/source/perforce/sweeper.go:36`, stored in a package var `evictTimeout` so tests can override.
- Test harness: `connectClient(t, s)` at `internal/mcp/delivery_test.go:16` (in-memory client session). `whoamiHandler(isAdmin, next)` at `internal/mcp/whoami_test_helper_test.go:14` — wrap every backend so `NewServer`'s startup whoami probe succeeds. Existing resource tests in `internal/mcp/resources_test.go`. Race-test pattern: `TestDo_ConcurrentReload_Race` at `internal/mcp/do_test.go:149`.

---

## File structure

- `internal/mcp/resources.go` (modify) — add two `AddResourceTemplate` registrations and the shared `readEntityByID` helper; route the `recent-jobs` `AddResource` handler through the cache.
- `internal/mcp/resource_cache.go` (create) — the `recentJobsCache` type, `defaultResourceCacheTTL`, `resourceCacheTTL` package var, and `resolveResourceCacheTTL()`.
- `internal/mcp/server.go` (modify) — add the `recentJobs recentJobsCache` field to `Server`; initialize it in `NewServer`.
- `internal/mcp/resource_templates_test.go` (create) — template registration, resolution-to-endpoint, and not-found tests.
- `internal/mcp/resource_cache_test.go` (create) — TTL hit/expiry/disabled, copy-invariant, and `-race` concurrency tests, plus a `resolveResourceCacheTTL` unit test.
- `README.md` (modify) — document `RELAY_MCP_RESOURCE_CACHE_TTL` in the MCP env section.

---

## PART 1 - Resource templates

### Task 1: Template registration appears in ListResourceTemplates

**Files:**
- Modify: `internal/mcp/resources.go:11-47` (inside `registerResourcesImpl`, after the two existing `AddResource` calls)
- Test: `internal/mcp/resource_templates_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/resource_templates_test.go`:

```go
package mcp

import (
	"context"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestResourceTemplates_Listed(t *testing.T) {
	b := newWhoamiBackend(t, true)
	s, err := NewServer(b.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	res, err := cs.ListResourceTemplates(context.Background(), &mcpsdk.ListResourceTemplatesParams{})
	require.NoError(t, err)

	got := map[string]bool{}
	for _, rt := range res.ResourceTemplates {
		got[rt.URITemplate] = true
	}
	require.True(t, got["relay://jobs/{id}"], "jobs template missing")
	require.True(t, got["relay://tasks/{id}"], "tasks template missing")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestResourceTemplates_Listed -v`
Expected: FAIL — neither template is registered yet (both map keys false).

- [ ] **Step 3: Write minimal implementation**

In `internal/mcp/resources.go`, inside `registerResourcesImpl`, after the `recent-jobs` `AddResource` block (after line 46, before the closing brace at line 47), add:

```go
	s.mcp.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "relay://jobs/{id}",
		Name:        "job",
		Title:       "Relay Job",
		Description: "A single relay job by id, including its task DAG.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		return s.readEntityByID(ctx, req.Params.URI, "relay://jobs/", "/v1/jobs/")
	})

	s.mcp.AddResourceTemplate(&mcpsdk.ResourceTemplate{
		URITemplate: "relay://tasks/{id}",
		Name:        "task",
		Title:       "Relay Task",
		Description: "A single relay task by id.",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		return s.readEntityByID(ctx, req.Params.URI, "relay://tasks/", "/v1/tasks/")
	})
```

Then add the shared helper at the bottom of `internal/mcp/resources.go` (this also satisfies Tasks 2 and 3; full not-found handling is included now so later tasks only add tests):

```go
// readEntityByID resolves a single-entity resource template. It strips prefix from
// the concrete URI to recover the id, GETs apiPath+id through the s.do chokepoint,
// and returns the entity JSON as a ReadResourceResult matching the fixed-resource
// content shape. A malformed/empty id or a backend 404 is reported to MCP clients
// as ResourceNotFoundError(uri); any other error is returned to the SDK as-is.
func (s *Server) readEntityByID(ctx context.Context, uri, prefix, apiPath string) (*mcpsdk.ReadResourceResult, error) {
	id := strings.TrimPrefix(uri, prefix)
	if id == "" || strings.ContainsRune(id, '/') {
		return nil, mcpsdk.ResourceNotFoundError(uri)
	}
	var entity map[string]any
	if err := s.do(ctx, "GET", apiPath+id, nil, &entity); err != nil {
		if MapError(err).Code == "not_found" {
			return nil, mcpsdk.ResourceNotFoundError(uri)
		}
		return nil, err
	}
	body, err := json.Marshal(entity)
	if err != nil {
		return nil, err
	}
	return &mcpsdk.ReadResourceResult{
		Contents: []*mcpsdk.ResourceContents{
			{URI: uri, MIMEType: "application/json", Text: string(body)},
		},
	}, nil
}
```

Add `"strings"` to the import block in `internal/mcp/resources.go` (it currently imports `context`, `encoding/json`, the SDK, and `relay/internal/relayclient`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestResourceTemplates_Listed -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/resources.go internal/mcp/resource_templates_test.go
git commit -m "feat(mcp): register relay://jobs/{id} and relay://tasks/{id} resource templates"
```

---

### Task 2: jobs and tasks templates resolve to the single-entity endpoint

**Files:**
- Test: `internal/mcp/resource_templates_test.go` (extend)
- (Implementation already landed in Task 1's `readEntityByID`.)

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/resource_templates_test.go` (the `encoding/json`, `net/http`, `net/http/httptest`, and `sync/atomic` imports are needed; add them to the file's import block):

```go
func TestResourceTemplate_Job_ResolvesEndpoint(t *testing.T) {
	var hit string
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		hit = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "status": "done"})
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	res, err := cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://jobs/j1"})
	require.NoError(t, err)
	require.Equal(t, "/v1/jobs/j1", hit)
	require.Len(t, res.Contents, 1)
	require.Equal(t, "relay://jobs/j1", res.Contents[0].URI)
	require.Equal(t, "application/json", res.Contents[0].MIMEType)
	require.Contains(t, res.Contents[0].Text, `"j1"`)
}

func TestResourceTemplate_Task_ResolvesEndpoint(t *testing.T) {
	var hit string
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		hit = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "t1", "status": "running"})
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	res, err := cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://tasks/t1"})
	require.NoError(t, err)
	require.Equal(t, "/v1/tasks/t1", hit)
	require.Contains(t, res.Contents[0].Text, `"t1"`)
}
```

- [ ] **Step 2: Run test to verify it fails or passes**

Run: `go test ./internal/mcp/... -run "TestResourceTemplate_Job_ResolvesEndpoint|TestResourceTemplate_Task_ResolvesEndpoint" -v`
Expected: PASS (implementation landed in Task 1). If FAIL, the helper or registration in Task 1 is wrong — fix there, not here. These tests lock the endpoint-resolution behavior so a future refactor cannot silently break it.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/resource_templates_test.go
git commit -m "test(mcp): assert job/task templates resolve to /v1/{jobs,tasks}/{id}"
```

---

### Task 3: backend 404 maps to ResourceNotFoundError

**Files:**
- Test: `internal/mcp/resource_templates_test.go` (extend)
- (Implementation already landed in Task 1's `readEntityByID`.)

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/resource_templates_test.go`:

```go
func TestResourceTemplate_NotFound(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	_, err = cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://jobs/missing"})
	require.Error(t, err)
	// The not-found contract carries the URI in the jsonrpc error data.
	require.Contains(t, err.Error(), "missing")
}

func TestResourceTemplate_EmptyID_NotFound(t *testing.T) {
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("backend should not be hit for empty id, got %s", r.URL.Path)
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)

	cs := connectClient(t, s)
	_, err = cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://jobs/"})
	require.Error(t, err)
}
```

Note: `relay://jobs/` may not match the `{id}` template regex (an empty variable). If the SDK does not route it to the handler at all, the SDK itself returns `ResourceNotFoundError` (server.go:799), so the test still passes. Either path satisfies the requirement; do not add code to force routing.

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run "TestResourceTemplate_NotFound|TestResourceTemplate_EmptyID_NotFound" -v`
Expected: PASS (the 404 branch and empty-id guard in `readEntityByID` from Task 1 handle these). If FAIL, fix `readEntityByID`.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/resource_templates_test.go
git commit -m "test(mcp): 404 and empty id on templates yield ResourceNotFoundError"
```

---

## PART 2 - TTL cache for relay://recent-jobs

### Task 4: resolveResourceCacheTTL reads RELAY_MCP_RESOURCE_CACHE_TTL

**Files:**
- Create: `internal/mcp/resource_cache.go`
- Test: `internal/mcp/resource_cache_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/resource_cache_test.go`:

```go
package mcp

import (
	"context"
	"sync"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestResolveResourceCacheTTL -v`
Expected: FAIL — `resolveResourceCacheTTL` and `defaultResourceCacheTTL` are undefined (compile error).

- [ ] **Step 3: Write minimal implementation**

Create `internal/mcp/resource_cache.go`:

```go
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
```

Note the difference from `resolveEvictTimeout`: a successfully-parsed `0` or negative value must be returned (disable), so do NOT reject on `d <= 0` the way the eviction helper does. Only an unset or unparseable value falls back to the default.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestResolveResourceCacheTTL -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/resource_cache.go internal/mcp/resource_cache_test.go
git commit -m "feat(mcp): resolve RELAY_MCP_RESOURCE_CACHE_TTL (default 10s, 0 disables)"
```

---

### Task 5: recentJobsCache.get caches a fresh value within TTL (one fetch)

**Files:**
- Modify: `internal/mcp/resource_cache.go`
- Test: `internal/mcp/resource_cache_test.go` (extend)

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/resource_cache_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestRecentJobsCache_HitWithinTTL -v`
Expected: FAIL — `recentJobsCache` and `.get` are undefined (compile error).

- [ ] **Step 3: Write minimal implementation**

Append to `internal/mcp/resource_cache.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestRecentJobsCache_HitWithinTTL -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/resource_cache.go internal/mcp/resource_cache_test.go
git commit -m "feat(mcp): recentJobsCache.get serves fresh value within TTL"
```

---

### Task 6: cache refetches after the TTL elapses (injectable clock)

**Files:**
- Test: `internal/mcp/resource_cache_test.go` (extend)
- (Implementation already landed in Task 5.)

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/resource_cache_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestRecentJobsCache_ExpiryRefetches -v`
Expected: PASS (Task 5 implementation; deterministic, no real sleeps). If FAIL, the staleness comparison in `get` is wrong.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/resource_cache_test.go
git commit -m "test(mcp): cache refetches after TTL elapses via injectable clock"
```

---

### Task 7: TTL<=0 disables caching, and a fetch error keeps the stale value

**Files:**
- Test: `internal/mcp/resource_cache_test.go` (extend)
- (Implementation already landed in Task 5.)

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/resource_cache_test.go`:

```go
func TestRecentJobsCache_DisabledAlwaysFetches(t *testing.T) {
	var calls int32
	fetch := func(ctx context.Context) ([]byte, *ToolError) {
		atomic.AddInt32(&calls, 1)
		return []byte(`{"items":[],"total":0}`), nil
	}
	c := &recentJobsCache{ttl: 0, now: time.Now}

	_, _ = c.get(context.Background(), fetch)
	_, _ = c.get(context.Background(), fetch)

	require.Equal(t, int32(2), atomic.LoadInt32(&calls), "ttl<=0 must refetch every read")
	require.Nil(t, c.body, "disabled cache must store nothing")
}

func TestRecentJobsCache_ErrorKeepsStale(t *testing.T) {
	good := []byte(`{"items":[{"id":"j1"}],"total":1}`)
	var fail bool
	fetch := func(ctx context.Context) ([]byte, *ToolError) {
		if fail {
			return nil, &ToolError{Code: "server_error", Message: "boom"}
		}
		return good, nil
	}

	var fake time.Time = time.Unix(0, 0)
	c := &recentJobsCache{ttl: 10 * time.Second, now: func() time.Time { return fake }}

	b1, terr := c.get(context.Background(), fetch)
	require.Nil(t, terr)
	require.Equal(t, string(good), string(b1))

	// Expire, then fail the refetch: stale value must remain, error returned.
	fail = true
	fake = fake.Add(time.Minute)
	_, terr = c.get(context.Background(), fetch)
	require.NotNil(t, terr)
	require.Equal(t, string(good), string(c.body), "stale value must survive a failed refetch")
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run "TestRecentJobsCache_DisabledAlwaysFetches|TestRecentJobsCache_ErrorKeepsStale" -v`
Expected: PASS (Task 5 implementation). If FAIL, the disable branch or the error-path early return in `get` is wrong.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/resource_cache_test.go
git commit -m "test(mcp): cache disabled-mode refetches and error keeps stale value"
```

---

### Task 8: get returns a copy, never the backing slice

**Files:**
- Test: `internal/mcp/resource_cache_test.go` (extend)
- (Implementation already landed in Task 5 via cloneBytes.)

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/resource_cache_test.go`:

```go
func TestRecentJobsCache_ReturnsCopy(t *testing.T) {
	fetch := func(ctx context.Context) ([]byte, *ToolError) {
		return []byte(`{"items":[],"total":0}`), nil
	}
	c := &recentJobsCache{ttl: time.Minute, now: time.Now}

	b1, terr := c.get(context.Background(), fetch)
	require.Nil(t, terr)
	// Mutate the returned slice; the cached value must be unaffected.
	for i := range b1 {
		b1[i] = 'X'
	}

	b2, terr := c.get(context.Background(), fetch)
	require.Nil(t, terr)
	require.Equal(t, `{"items":[],"total":0}`, string(b2), "mutating a returned slice must not corrupt the cache")
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestRecentJobsCache_ReturnsCopy -v`
Expected: PASS (cloneBytes in Task 5 prevents the interior-pointer leak). If FAIL, `get` is handing out `c.body` directly — fix to return `cloneBytes(c.body)`.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/resource_cache_test.go
git commit -m "test(mcp): cache.get returns a copy (no interior pointer across lock)"
```

---

### Task 9: concurrent reads are race-clean

**Files:**
- Test: `internal/mcp/resource_cache_test.go` (extend)
- (Implementation already landed in Task 5.)

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/resource_cache_test.go` (modeled on `TestDo_ConcurrentReload_Race` at do_test.go:149):

```go
func TestRecentJobsCache_ConcurrentReads_Race(t *testing.T) {
	var calls int32
	fetch := func(ctx context.Context) ([]byte, *ToolError) {
		atomic.AddInt32(&calls, 1)
		return []byte(`{"items":[],"total":0}`), nil
	}
	c := &recentJobsCache{ttl: time.Minute, now: time.Now}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, terr := c.get(context.Background(), fetch)
			require.Nil(t, terr)
			require.Equal(t, `{"items":[],"total":0}`, string(b))
		}()
	}
	wg.Wait()
	require.GreaterOrEqual(t, atomic.LoadInt32(&calls), int32(1))
}
```

- [ ] **Step 2: Run test to verify it passes (under -race)**

On a machine with a working cgo race toolchain (Docker; see verify section):
Run: `go test -race ./internal/mcp/... -run TestRecentJobsCache_ConcurrentReads_Race -v`
Expected: PASS, no data-race report.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/resource_cache_test.go
git commit -m "test(mcp): concurrent cache reads are race-clean"
```

---

### Task 10: wire the cache onto Server and route recent-jobs through it

**Files:**
- Modify: `internal/mcp/server.go:18-28` (Server struct) and `internal/mcp/server.go:46-49` (NewServer init)
- Modify: `internal/mcp/resources.go:36-46` (recent-jobs handler)
- Test: `internal/mcp/resource_templates_test.go` (extend — end-to-end cache-hit through the registered resource)

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/resource_templates_test.go`:

```go
func TestRecentJobsResource_CachedAcrossReads(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(whoamiHandler(true, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/jobs" {
			atomic.AddInt32(&calls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{"id": "j1"}},
				"total": 1,
			})
			return
		}
		http.Error(w, "unexpected: "+r.URL.Path, http.StatusNotFound)
	}))
	defer srv.Close()

	s, err := NewServer(srv.URL, "t")
	require.NoError(t, err)
	// Force a long TTL regardless of the ambient env value.
	s.recentJobs.ttl = time.Minute

	cs := connectClient(t, s)
	_, err = cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://recent-jobs"})
	require.NoError(t, err)
	_, err = cs.ReadResource(context.Background(), &mcpsdk.ReadResourceParams{URI: "relay://recent-jobs"})
	require.NoError(t, err)

	require.Equal(t, int32(1), atomic.LoadInt32(&calls), "second recent-jobs read should hit the cache")
}
```

Add `"time"` to the import block of `internal/mcp/resource_templates_test.go` if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestRecentJobsResource_CachedAcrossReads -v`
Expected: FAIL — `s.recentJobs` field does not exist (compile error), and the handler still calls `s.readRecentJobs` directly so both reads would hit the backend.

- [ ] **Step 3: Write minimal implementation**

In `internal/mcp/server.go`, add a field to the `Server` struct (after `reloadToken` at line 27, before the closing brace at line 28):

```go
	// recentJobs caches the relay://recent-jobs resource for resourceCacheTTL so
	// repeated polls within a session do not refetch GET /v1/jobs?limit=20.
	recentJobs recentJobsCache
```

In `NewServer`, initialize it where `s` is constructed (lines 46-49). Replace:

```go
	s := &Server{
		client: relayclient.NewClient(serverURL, token),
		mcp:    mcpServer,
	}
```

with:

```go
	s := &Server{
		client:     relayclient.NewClient(serverURL, token),
		mcp:        mcpServer,
		recentJobs: recentJobsCache{ttl: resourceCacheTTL, now: time.Now},
	}
```

(`time` is already imported in server.go.)

In `internal/mcp/resources.go`, change the `recent-jobs` `AddResource` handler body (lines 36-46) from calling `s.readRecentJobs(ctx)` to routing through the cache:

```go
	}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		body, terr := s.recentJobs.get(ctx, s.readRecentJobs)
		if terr != nil {
			return nil, terr
		}
		return &mcpsdk.ReadResourceResult{
			Contents: []*mcpsdk.ResourceContents{
				{URI: "relay://recent-jobs", MIMEType: "application/json", Text: string(body)},
			},
		}, nil
	})
```

(`s.readRecentJobs` already has signature `func(context.Context) ([]byte, *ToolError)`, matching `get`'s `fetch` parameter. `readRecentJobs` itself is unchanged.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestRecentJobsResource_CachedAcrossReads -v`
Expected: PASS.

- [ ] **Step 5: Run the full mcp package + the original recent-jobs test**

Run: `go test ./internal/mcp/... -v`
Expected: PASS, including the pre-existing `TestResource_RecentJobs` (which calls `s.readRecentJobs` directly and is unaffected by the cache wiring).

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/server.go internal/mcp/resources.go internal/mcp/resource_templates_test.go
git commit -m "feat(mcp): route relay://recent-jobs through the TTL cache"
```

---

### Task 11: document RELAY_MCP_RESOURCE_CACHE_TTL in the README

**Files:**
- Modify: `README.md` (MCP env section)

- [ ] **Step 1: Find the MCP env documentation**

Run: `git grep -n "RELAY_URL" README.md` and locate the section that lists MCP-related env vars (where the relay MCP server's `RELAY_URL` / token env are documented).

- [ ] **Step 2: Add the env var doc**

Add a row/line documenting:

```
RELAY_MCP_RESOURCE_CACHE_TTL - Go duration for the relay://recent-jobs resource cache (default 10s). Set to 0 to disable caching (every read refetches).
```

Match the surrounding formatting (table row vs bullet list) exactly; do not reformat adjacent entries.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: document RELAY_MCP_RESOURCE_CACHE_TTL"
```

---

## Verify commands (full suite)

Run from the worktree root `D:\dev\relay\.claude\worktrees\happy-mendel-18687f`:

- `go test ./internal/mcp/... -v` — all unit tests (Windows OK; no Docker, no build tags).
- `go vet ./internal/mcp/...` — vet clean.
- `go build ./...` — compiles.
- Race (the concurrency test needs a cgo race toolchain — run in Docker per the project's race-detector note, or with MSYS2 mingw64 gcc on Windows):
  `go test -race ./internal/mcp/... -run TestRecentJobsCache_ConcurrentReads_Race -v`

There is no integration tag and no `make generate` step (no `.sql`/`.proto` edits).

---

## Self-review

**Spec coverage:**
- Part 1 templates registered (`relay://jobs/{id}`, `relay://tasks/{id}`) — Task 1. Resolve to GET endpoints — Task 2. 404 -> ResourceNotFoundError, empty id guard — Task 3. Shared `readEntityByID` helper, prefix-strip extraction from `req.Params.URI`, same mime/content shape — Task 1. Worker template explicitly out of scope per spec.
- Part 2 cache: `recentJobsCache` struct with `sync.Mutex`, `body`/`fetchedAt`, injectable `now`, resolved `ttl` — Tasks 4, 5. TTL<=0 always refetch no store — Task 7. Fresh-within-TTL returns a copy — Tasks 5, 8. Stale/empty refetch, keep stale on error — Tasks 6, 7. Resolve TTL once from env at construction, default 10s, 0 disables — Tasks 4, 10. Wire recent-jobs handler through cache — Task 10.
- Test seam: injectable `now` clock (Tasks 6, 7), fake backend with request counter (Tasks 2, 10), all six acceptance assertions (a) template resolves and hits GET — Task 2; (b) not-found -> ResourceNotFoundError — Task 3; (c) two reads within TTL -> one GET — Tasks 5, 10; (d) read after clock advance -> second GET — Task 6; (e) TTL=0 disables -> every read GETs — Task 7; (f) concurrent reads race-clean — Task 9.
- Backend-only, no build gate, verify commands listed — declared at top and in Verify section.
- Env var documented — Task 11.

**Placeholder scan:** every code step shows full real code; no TBD/TODO/"handle edge cases".

**Type consistency:** `recentJobsCache` fields `body []byte`, `fetchedAt time.Time`, `ttl time.Duration`, `now func() time.Time`; method `get(ctx, func(context.Context)([]byte,*ToolError)) ([]byte,*ToolError)`; helper `readEntityByID(ctx, uri, prefix, apiPath string) (*mcpsdk.ReadResourceResult, error)`; package var `resourceCacheTTL`, const `defaultResourceCacheTTL`, func `resolveResourceCacheTTL()`. `s.readRecentJobs` signature matches `get`'s `fetch` param. All consistent across tasks.
