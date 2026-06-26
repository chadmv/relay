---
title: MCP resources have no caching and no resource templates
type: bug
status: closed
created: 2026-05-09
closed: 2026-06-25
resolution: fixed
source: MCP server session retro
---

# MCP resources have no caching and no resource templates

## Summary
No resource templates (`relay://jobs/{id}`) in v1. Resources are fetched fresh on every read with no caching.

## Proposal
Add resource templates for common entities (jobs, tasks, workers) so clients can request individual resources by ID. Add optional short-lived caching (e.g. 5–10 s TTL) for the `relay://recent-jobs` resource to reduce redundant API calls during an active session.

## Acceptance / Done When
- `relay://jobs/{id}`, `relay://tasks/{id}` resource templates registered and resolvable.
- `relay://recent-jobs` has a configurable TTL cache (default 10 s, disable via flag).

## Related
- `internal/mcp/resources.go` — current resource registrations
- `internal/mcp/server.go` — `Server` struct (add cache field here)

## Resolution
Fixed 2026-06-25. Both acceptance criteria met. PART 1: registered `relay://jobs/{id}` and
`relay://tasks/{id}` resource templates via the go-sdk `AddResourceTemplate` (RFC 6570
`URITemplate`); a shared `readEntityByID` helper extracts the id from `req.Params.URI`,
`url.PathEscape`-es it to a single path segment (security-hardened against path-injection /
encoded-traversal per review), GETs `GET /v1/jobs/{id}` or `GET /v1/tasks/{id}` through the `s.do`
chokepoint (inherits 401 reload), and maps a backend 404 to `mcpsdk.ResourceNotFoundError`.
PART 2: `relay://recent-jobs` now goes through a mutex-guarded `recentJobsCache` (single value +
timestamp, injectable `now` clock, copy-out so no interior pointer escapes the lock) with TTL from
`RELAY_MCP_RESOURCE_CACHE_TTL` (Go duration, default 10s; a parsed 0/negative disables - distinct
from the eviction-timeout fallback). `relay://workers/{id}` was deliberately scoped out (in the
proposal but not the acceptance; `GET /v1/workers/{id}` exists, so it is a trivial follow-up).
Documented in README.md. Unit tests (template resolution, not-found contract via unwrapped
`*jsonrpc.Error` code, cache hit/expiry/disabled/error-keeps-stale, copy-invariant proven RED,
path-injection regression proven RED, concurrent reads) green on Windows + Docker; `-race` clean in
Docker; `go vet` clean. Adversarial review caught a Medium path-injection finding (unescaped id);
fixed with `url.PathEscape` + a RED-proven regression test; re-review clean. Integration suite does
not cover resources (reported, not silently skipped).
