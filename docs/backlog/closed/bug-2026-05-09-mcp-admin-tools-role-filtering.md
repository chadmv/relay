---
title: MCP admin-only tools have no discovery-time role filtering
type: bug
status: closed
created: 2026-05-09
closed: 2026-06-25
resolution: fixed
source: MCP server session retro
---

# MCP admin-only tools have no discovery-time role filtering

## Summary
Admin-only tools (`relay_list_reservations`) are registered for all users with no discovery-time filtering by role. Non-admins only learn they lack access after calling the tool and receiving a `forbidden` error.

(`relay_run_schedule_now` was previously listed here as admin-only, but the
run-now contract was reconciled to owner-or-admin - see
[[bug-2026-06-18-run-now-not-admin-gated]] - so it is correctly available to
non-admins and is no longer in scope for role filtering.)

## Proposal
At server startup (after `whoami` resolves the logged-in user), conditionally register admin-only tools only when `is_admin == true`. Non-admin sessions would not see these tools in the MCP tool list at all, reducing confusion. The current `forbidden` error path would remain as a belt-and-suspenders fallback.

## Acceptance / Done When
- Non-admin MCP session: `relay_list_reservations` absent from tool list.
- Admin MCP session: it is present and functional.
- `whoami` failure at startup still exits cleanly (existing behavior unchanged).

## Related
- `internal/mcp/server.go` — `registerTools` and `NewServer`
- `internal/mcp/reservations.go`, `internal/mcp/run_now.go`

## Resolution
Fixed 2026-06-25. `NewServer` (`internal/mcp/server.go`) now resolves the caller identity once at
startup via the existing `callWhoami` (`GET /v1/users/me`), stores `isAdmin`, and registers the
admin-only `relay_list_reservations` tool only when `is_admin == true`. A fail-closed comma-ok
assertion (`who["is_admin"].(bool)`) defaults to NOT registering on any ambiguous response shape.
A startup whoami failure returns the error from `NewServer`, preserving the existing clean exit
(`internal/cli/mcp.go` untouched). Discovery filtering is cosmetic only - the authoritative
boundary remains the server-side `auth(admin(...))` gate on `GET /v1/reservations`
(`internal/api/server.go`) plus the `forbidden` ToolError fallback in `reservations.go`, both
unchanged. New unit tests over the real SDK `ListTools` discovery surface (non-admin absent /
admin present + functional / whoami-failure clean) plus integration assertions; ~16 existing MCP
test files repaired for the new startup probe via a shared `whoamiHandler`/`newWhoamiBackend`
helper. Full unit + Docker + integration suites green; `go vet` clean; adversarial review found no
high/medium/low findings.
