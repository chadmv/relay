---
date: 2026-06-25
topic: mcp-resource-templates-cache
branch: claude/happy-mendel-18687f
pr: autopilot (this branch)
---

# Session Retro: 2026-06-25 - MCP resource templates and recent-jobs cache

**TL;DR:** Fixed the bug "MCP resources have no caching and no resource templates". Two parts.
PART 1 added `relay://jobs/{id}` and `relay://tasks/{id}` resource templates via the go-sdk
`AddResourceTemplate`, backed by a shared `readEntityByID` helper that recovers the id from
`req.Params.URI`, `url.PathEscape`-es it to a single path segment, GETs the single-entity endpoint
through the `s.do` chokepoint, and maps a backend 404 to `ResourceNotFoundError`. PART 2 made
`relay://recent-jobs` serve from a mutex-guarded single-value cache (value + timestamp, injectable
`now` clock, copy-out) with a TTL from `RELAY_MCP_RESOURCE_CACHE_TTL` (default 10s; 0/negative
disables). `relay://workers/{id}` was deliberately scoped out (proposed but not accepted; filed as
a trivial follow-up this cycle). Tests green Windows + Docker, `-race` clean in Docker, vet clean.

## What Was Built

- **Templates** `internal/mcp/resources.go` - two `AddResourceTemplate` registrations plus the
  shared `readEntityByID(ctx, uri, prefix, apiPath)` helper.
- **Cache** `internal/mcp/resource_cache.go` - `recentJobsCache` (single value + timestamp,
  injectable clock, copy-out under mutex); TTL parsed from `RELAY_MCP_RESOURCE_CACHE_TTL` in
  `internal/mcp/server.go`.
- **Tests** for both, plus a RED-proven path-injection regression test (see below).
- **Docs** README.md updated for the new templates, the cache env var, and the workers/{id}
  non-goal.

## Lesson (key): escape any client-supplied value into a single path segment - never denylist

Code review caught a MEDIUM path-injection finding. The first cut concatenated the client-supplied
id straight into the GET path with only a `/`-denylist guard. That let an encoded-traversal id
(`..%2fadmin`) decode downstream into `/v1/jobs/../admin`. It was not exploitable today (the stdlib
router plus global-read authz contain it), but a denylist-plus-downstream-router-quirk guard is a
fragile anti-pattern - one router change or one new escape character away from a real traversal.

The fix is the durable rule: **when interpolating ANY client-supplied value into a request
path or URL, escape it to a single literal segment with `url.PathEscape` - do not rely on a
denylist of bad characters plus whatever the downstream router happens to do.** `PathEscape`
encodes `/`, `?`, `#`, and friends, so the value can only ever be one path segment: traversal and
query-string injection both become impossible by construction rather than by containment. This is
the same shape as the project's other "make it safe by construction, not by review" invariants
(single JSON entry point, single bounded sender). The regression test was proven RED against the
pre-fix concatenation and passes with the escape in place.

## Lesson: verify the finding, do not blindly implement it

The reviewer flagged the finding as *query-string injection*. The engineer checked it against the
SDK before implementing and found the actual vector was different: the go-sdk's RFC6570 template
matcher rejects `?`-bearing URIs, so a `?`-injection never reaches the handler - the real exposure
was encoded path traversal. The escape fix covers both, so the right thing still shipped, but the
*reason* in the test and the comment is now accurate. This is the standing bar working in both
directions: review caught a genuine defect, and the engineer corrected the reviewer's stated
exploit vector rather than copying it into the commit message. A finding is a hypothesis to verify,
not a script to execute.

## Technical decision: env-configured TTL with a disable escape hatch

The recent-jobs cache TTL is read from `RELAY_MCP_RESOURCE_CACHE_TTL` (default 10s). A parsed value
of 0 or negative disables caching entirely. This keeps the default fast-path cheap (10s of staleness
on a "20 most recent jobs" view is harmless) while giving operators a no-rebuild way to tune
freshness or turn the cache off outright for debugging or low-latency needs. Matches the project's
existing env-knob convention (e.g. `RELAY_WORKER_GRACE_WINDOW`) - operational behavior that a
deployer might reasonably want to vary lives in an env var, not a hardcoded const.

## Non-goal: the single-entity templates intentionally do not cache

PART 2 caches only `relay://recent-jobs` - a single fixed resource with one value. The `{id}`
templates deliberately do not cache: their keyspace is unbounded (one entry per job/task/worker id),
so a correct cache would need an LRU or per-key TTL map with its own eviction and memory bound. That
is real surface area for a marginal win on a per-id read that is already a single backend GET.
Documented as a non-goal in README rather than filed as backlog - it is not specific or actionable
enough to be a task, and adding it speculatively would violate YAGNI. If a concrete need appears
(a hot-id read path showing up in profiling), revisit then with the keyspace bound as the headline
design question.

## Backlog Triage

**Filed one item.** `docs/backlog/idea-2026-06-25-mcp-workers-resource-template.md` (type idea,
priority low): add the `relay://workers/{id}` resource template. Genuinely actionable and now
trivial - `GET /v1/workers/{id}` exists (`internal/api/server.go` `handleGetWorker`) and the
`readEntityByID` helper is reusable as-is, so this is a one-template-plus-one-helper-call addition
mirroring the jobs/tasks templates shipped this cycle. Verified the endpoint exists before filing.
Low priority because it is a small parity follow-up, not a gap with user pain.

**Did not file** the single-entity-template caching idea - captured above as a documented non-goal
rather than a backlog item because it is not specific or actionable without a concrete hot-path
need and an LRU/eviction design.
