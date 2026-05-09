---
title: MCP resources have no caching and no resource templates
type: bug
status: open
created: 2026-05-09
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
