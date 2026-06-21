---
title: MCP admin-only tools have no discovery-time role filtering
type: bug
status: open
created: 2026-05-09
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
