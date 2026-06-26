# MCP Resource Templates and Recent-Jobs TTL Cache - Design

- Date: 2026-06-25
- Status: Draft (autonomous Phase 1 spec)
- Backlog: `docs/backlog/bug-2026-05-09-mcp-resources-caching-templates.md`
- Owner area: `internal/mcp` (MCP client/resource layer; no relay-server change)

## Problem

The relay MCP server exposes only two fixed resources (`relay://server-info`,
`relay://recent-jobs`) via `(*mcpsdk.Server).AddResource`. Two gaps:

1. No way to read a single entity by id. A client that knows a job or task id
   must fall through to a tool call rather than a direct resource read. The MCP
   spec supports URI-templated resources (`relay://jobs/{id}`) exactly for this.
2. `relay://recent-jobs` issues a fresh `GET /v1/jobs?limit=20` on every read.
   During an active session a client may poll it repeatedly, producing redundant
   backend calls and load.

## Grounding (verified against code, not pointers)

- SDK in use: `github.com/modelcontextprotocol/go-sdk v1.6.0` (go.mod line 9).
  Template dep `github.com/yosida95/uritemplate/v3` is already present.
- Template API exists: `func (s *Server) AddResourceTemplate(t *ResourceTemplate,
  h ResourceHandler)` (`mcp/server.go:538`). `ResourceTemplate` carries
  `Name`, `Title`, `Description`, `MIMEType`, and `URITemplate` (RFC 6570).
  `ResourceHandler` is the same signature already used for `AddResource`:
  `func(context.Context, *ReadResourceRequest) (*ReadResourceResult, error)`
  (`mcp/resource.go:38`).
- Routing: on a `resources/read`, the SDK's `readResource` (`mcp/server.go:791`)
  first tries exact resource URIs, then iterates templates calling
  `serverResourceTemplate.Matches(uri)` (regex from the URI template,
  `mcp/resource.go:175`). The matching handler is invoked with the concrete URI
  in `req.Params.URI`. The SDK does NOT pass extracted variables to the handler;
  the handler extracts the id from `req.Params.URI` itself.
- Not-found contract: handlers signal a missing resource by returning
  `mcpsdk.ResourceNotFoundError(uri)` (`mcp/resource.go:42`), a `*jsonrpc.Error`
  with code `CodeResourceNotFound`. An unregistered URI already returns this from
  the SDK.
- Single-entity endpoints both exist:
  - `GET /v1/jobs/{id}` -> `handleGetJob` (`internal/api/server.go:118`);
    returns the full job with its DAG.
  - `GET /v1/tasks/{id}` -> `handleGetTask` (`internal/api/server.go:123`).
  Both read routes are intentionally global to any authenticated user (see the
  authz note at `server.go:110-114`), so an MCP session with a valid token can
  read any id; no new authz surface is introduced.
- Request chokepoint: every fetch goes through `(*Server).do` (`internal/mcp/do.go:16`),
  which wraps `relayclient.Client.Do`, handles the 401 reload-retry, and returns
  a `*relayclient.ResponseError`. `MapError` (`internal/mcp/errors.go:20`) maps a
  404 to `ToolError{Code:"not_found"}`.
- Existing resource content shape: `*ResourceContents{URI, MIMEType:"application/json",
  Text: <json>}`. `readRecentJobs` returns `{"items":[...],"total":N}` from a
  `relayclient.PageEnvelope[map[string]any]`.
- Token concurrency precedent: `relayclient.Client` guards its token with a
  `sync.RWMutex` (`internal/relayclient/client.go:29`), Token()/SetToken().
- Env-duration precedent: `RELAY_EVICTION_TIMEOUT` / `RELAY_WORKER_GRACE_WINDOW`
  resolved once via `time.ParseDuration`, fallback on unset/unparseable, as a
  package var so tests can override (`internal/agent/source/perforce/sweeper.go:36`).
- Test harness: `connectClient(t, s)` (`internal/mcp/delivery_test.go:16`) returns
  a real `*mcpsdk.ClientSession` wired to the server in-memory, so tests can
  exercise `ReadResource` / `ListResourceTemplates` end to end. Backends are
  `httptest.Server`s wrapped by `whoamiHandler` so `NewServer`'s startup probe
  passes (`internal/mcp/whoami_test_helper_test.go`).

## Scope decision

- In scope: `relay://jobs/{id}` and `relay://tasks/{id}` templates (acceptance
  lists both; both backend GET endpoints exist and need no API change).
- Out of scope: `relay://workers/{id}`. The proposal mentions workers but the
  acceptance criteria do not; per the stated scoping rule we limit to jobs+tasks.
  A worker template is a trivial follow-up (the endpoint `GET /v1/workers/{id}`
  exists) but is not specified here.
- No relay-server / API change is required. This is entirely `internal/mcp`.

---

## Part 1 - Resource templates

### Registration

In `registerResourcesImpl` (`internal/mcp/resources.go`), after the two existing
`AddResource` calls, add two `AddResourceTemplate` calls:

- `relay://jobs/{id}` - Name `job`, Title "Relay Job", Description "A single
  relay job by id, including its task DAG.", MIMEType `application/json`.
- `relay://tasks/{id}` - Name `task`, Title "Relay Task", Description "A single
  relay task by id.", MIMEType `application/json`.

Each registers a `ResourceHandler`. The `{id}` segment is a simple RFC 6570
variable; the SDK builds the matching regex from it.

### Resolution flow (per template)

1. Read the concrete URI from `req.Params.URI`.
2. Extract the id. Use the SDK's own template (`uritemplate.New(template).Match(uri)`)
   to pull the `id` variable, OR a small local strip of the known fixed prefix
   (`strings.TrimPrefix(uri, "relay://jobs/")`). Prefer the prefix strip for
   simplicity since the template is a fixed single-variable suffix; reject an
   empty or slash-containing remainder as not-found to avoid a malformed
   downstream path. (Implementation detail; pick one, keep it to a couple lines.)
3. Validate the id is non-empty. If empty -> return
   `mcpsdk.ResourceNotFoundError(uri)`.
4. Fetch via the existing chokepoint:
   `s.do(ctx, "GET", "/v1/jobs/"+id, nil, &raw)` (tasks: `/v1/tasks/`+id),
   decoding into a `map[string]any` (mirrors `readRecentJobs`, preserving
   whatever fields the API returns including the DAG).
5. On error, branch on the mapped code:
   - 404 (`MapError(err).Code == "not_found"`) -> return
     `mcpsdk.ResourceNotFoundError(uri)` so MCP clients get the standard
     resource-not-found error rather than a generic failure.
   - Any other error -> return the SDK a plain `error` (the handler may return
     the raw `do` error or a wrapped one); the SDK surfaces it to the client.
     We do NOT convert to `ToolError` here - `ToolError` is the tool surface;
     resources use the SDK's error path.
6. On success, `json.Marshal` the map and return
   `&ReadResourceResult{Contents: []*ResourceContents{{URI: uri,
   MIMEType: "application/json", Text: string(body)}}}`, matching the existing
   resource format exactly.

### Why extract in the handler

The SDK passes only `req.Params.URI`; it does not hand the handler the parsed
template variables (confirmed at `mcp/server.go:791-834`). Extraction is the
handler's responsibility. This is the SDK's real mechanism, not an invention.

### Shared helper

The two handlers differ only in URI prefix and backend path. A single private
helper - e.g. `readEntityByID(ctx, uri, prefix, apiPath)` returning
`(*ReadResourceResult, error)` - keeps both registrations to a few lines and
ensures identical not-found/format behavior. (Shape is implementation detail;
the spec requires both templates resolve through one consistent path.)

---

## Part 2 - TTL cache for `relay://recent-jobs`

### Goal

Avoid a second `GET /v1/jobs?limit=20` within a short window. Only
`relay://recent-jobs` is cached. `server-info` and the per-id templates are NOT
cached (per-id reads are point lookups; server-info is cheap and identity-ish).

### Structure

A small cache type owned by `Server`, holding a single cached value:

```
type recentJobsCache struct {
    mu        sync.Mutex
    body      []byte    // last successful marshaled JSON
    fetchedAt time.Time // zero => no value yet
    ttl       time.Duration
    now       func() time.Time // injectable clock; defaults to time.Now
}
```

- One cached value with a timestamp (the resource has no parameters, so a single
  slot suffices - no keyed map needed).
- `sync.Mutex` (not RWMutex): a cache miss must refetch and write under the same
  critical section, and the hot path still mutates on miss, so a plain mutex is
  simpler and correct. (The relayclient RWMutex precedent applies to a
  read-mostly token; here writes are common on expiry.) Single-flight of
  concurrent misses (one fetch, others wait then see the fresh value) is a
  desirable side effect of holding the lock across the fetch; acceptable because
  the backend call is fast and this is a single-process stdio server. If a fetch
  under the lock is judged too coarse, an alternative is documented below.

### Read path (`Get(ctx, fetch func) ([]byte, *ToolError)`)

1. Lock.
2. If `ttl <= 0` (disabled) -> fetch, return result WITHOUT storing. Unlock.
3. Else if `body != nil` and `now().Sub(fetchedAt) < ttl` -> return a COPY of
   `body` (see invariant below). Unlock. No backend call.
4. Else -> call `fetch` (the existing `readRecentJobs` body builder), and on
   success store `body` + `fetchedAt = now()`, return a copy. On error, leave the
   previous (stale) value untouched and return the error - do not poison the
   cache with a partial/empty body. Unlock.

The existing `readRecentJobs` keeps its current logic (the fetch+marshal) and the
resource handler routes through the cache: the `recent-jobs` `AddResource`
handler calls `cache.Get(ctx, s.readRecentJobs)` instead of `s.readRecentJobs`
directly.

### Wiring on Server

- Add a field to `Server`: `recentJobs *recentJobsCache` (or value).
- Initialize in `NewServer` with `ttl: resolveResourceCacheTTL()` and
  `now: time.Now`.
- The handler in `registerResourcesImpl` for `relay://recent-jobs` changes from
  calling `s.readRecentJobs(ctx)` to `s.recentJobs.Get(ctx, s.readRecentJobs)`.

### Configurability (env var)

Per the maintainer preference for env-configurable operational knobs (matching
`RELAY_WORKER_GRACE_WINDOW` / `RELAY_EVICTION_TIMEOUT`):

- Env var: `RELAY_MCP_RESOURCE_CACHE_TTL`, a Go duration string (e.g. `10s`,
  `30s`, `0`).
- Default when unset or unparseable: `10s` (`defaultResourceCacheTTL`).
- Disable semantics: a parsed value of `0` (or negative) disables caching -
  every read refetches and nothing is stored. The "disable via flag" wording in
  the acceptance is satisfied via this env knob (consistent with the project's
  env-over-flag convention); document `RELAY_MCP_RESOURCE_CACHE_TTL=0` as the
  disable switch in README's MCP env section.
- Resolve once at construction via a package-level `resolveResourceCacheTTL()`
  helper modeled on `resolveEvictTimeout`; expose the default as a package var so
  tests can shorten/override without env mutation. The injectable `now` clock is
  the primary test seam for expiry.

---

## Invariants and safety

This change lives in the MCP client/resource layer and touches no relay-server
write path, so the server-side Invariants (epoch fence, job-spec pipeline, gRPC
sender, identity-checked teardown, single JSON entry point) are not in play.
Confirm: no `tasks.status` / `task_logs` writes, no stream sends, no store calls.

Relevant local rules:

- No interior pointers across locks. `recentJobsCache.Get` must return a COPY of
  the cached `[]byte`, never the internal slice, so a caller cannot mutate the
  cached bytes after the lock is released. (The bytes are then passed to the SDK
  as a string; `string(copy)` materializes an immutable copy anyway, but the
  cache method should not hand out its backing slice as a mutable value.)
- Thread-safety. The MCP server can dispatch concurrent `resources/read`
  requests; all cache access is under the mutex. Verified under `-race`.
- Single chokepoint. Template fetches go through `s.do`, inheriting the 401
  reload-retry and `ResponseError` handling for free. No new HTTP path bypasses
  it.

---

## Acceptance criteria

1. `relay://jobs/{id}` and `relay://tasks/{id}` templates are registered and
   appear in `ListResourceTemplates`.
2. Reading `relay://jobs/<id>` issues `GET /v1/jobs/<id>` and returns the job
   JSON (incl. DAG) as `application/json` resource content; `relay://tasks/<id>`
   issues `GET /v1/tasks/<id>` likewise.
3. Reading a template URI whose id the backend 404s returns an MCP
   resource-not-found error (`CodeResourceNotFound`), not a generic failure.
4. With default TTL, two `relay://recent-jobs` reads inside the window produce
   exactly one `GET /v1/jobs?limit=20` (second served from cache).
5. After the TTL elapses, the next `relay://recent-jobs` read refetches (a second
   backend GET is observed).
6. With `RELAY_MCP_RESOURCE_CACHE_TTL=0` (cache disabled), every
   `relay://recent-jobs` read produces a backend GET (no caching).
7. Concurrent `relay://recent-jobs` reads are race-clean under `-race`.

## Test strategy

Use the existing harness: `httptest.Server` wrapped by `whoamiHandler`, `NewServer`,
and `connectClient` for end-to-end `ReadResource` / `ListResourceTemplates`.

- Templates registered: `connectClient(...).ListResourceTemplates(ctx, nil)` and
  assert both URI templates are present.
- Template resolves to the right endpoint: backend records the request path;
  call `ReadResource(ctx, &ReadResourceParams{URI:"relay://jobs/j1"})` (or the
  internal handler directly, mirroring `TestResource_RecentJobs` which calls
  `s.readRecentJobs`). Assert path `/v1/jobs/j1` was hit and the returned content
  contains the job fields. Repeat for tasks -> `/v1/tasks/t1`.
- Not-found: backend returns 404 for an unknown id; assert the read yields a
  resource-not-found error (assert the `*jsonrpc.Error` code, or that
  `ReadResource` returns an error whose data carries the URI).
- Cache hit within TTL: backend increments a request counter on
  `GET /v1/jobs?limit=20`. Two reads via the cached handler; assert counter == 1.
  Drive reads through `s.recentJobs.Get(ctx, s.readRecentJobs)` or the SDK
  `ReadResource` for `relay://recent-jobs`.
- Expiry: inject a fake `now` (the cache's `now` field). First read (counter 1),
  advance the fake clock past TTL, second read (counter 2). Deterministic, no
  sleeps. (A tiny real TTL like 1ms + sleep is the fallback if the clock seam is
  not wired, but the injectable clock is preferred.)
- Disabled: construct the cache with `ttl: 0` (or set env in a `resolveResourceCacheTTL`
  unit test); two reads -> counter == 2; assert nothing is stored.
- Race: N concurrent reads through the cache under `-race`, modeled on
  `TestDo_ConcurrentReload_Race`.
- Copy invariant: read the cached body, mutate the returned slice, read again,
  assert the second read is unaffected (proves no shared backing slice escapes).

## Files touched (implementation, not this doc)

- `internal/mcp/resources.go` - add two `AddResourceTemplate` registrations + the
  shared `readEntityByID` helper; route `recent-jobs` through the cache.
- `internal/mcp/server.go` - add the `recentJobs` cache field; initialize in
  `NewServer`.
- New `internal/mcp/resource_cache.go` (or inline in resources.go) - the
  `recentJobsCache` type and `resolveResourceCacheTTL`.
- Tests: extend `internal/mcp/resources_test.go` and/or new
  `internal/mcp/resource_templates_test.go`, `internal/mcp/resource_cache_test.go`.
- README MCP env section - document `RELAY_MCP_RESOURCE_CACHE_TTL`.

## Open question / alternative

- Cache lock granularity: holding the mutex across the backend fetch on a miss
  serializes concurrent misses (single-flight). For this single-process stdio
  server with a fast backend that is acceptable and simplest. If a future
  multi-tenant/HTTP transport makes this contention matter, switch to a
  "snapshot under lock, fetch outside lock, re-lock to store (last-writer-wins)"
  pattern. Flagging, not blocking; default to the simple version.
