---
date: 2026-06-25
topic: mcp-discovery-role-filtering
branch: claude/happy-mendel-18687f
pr: "autopilot"
---

# Session Retro: 2026-06-25 - MCP discovery-time role filtering

**TL;DR:** Closed the bug "MCP admin-only tools have no discovery-time role filtering."
`NewServer` (internal/mcp/server.go) now resolves caller identity once at startup via the
existing `callWhoami` (GET /v1/users/me), stores `isAdmin`, and registers the admin-only
`relay_list_reservations` tool only when `is_admin == true` (fail-closed comma-ok assertion).
A startup whoami failure returns the error from `NewServer`; `cli/mcp.go` already exits cleanly
on that error and was untouched. Discovery filtering is cosmetic - the server-side
`auth(admin(...))` gate plus the forbidden `ToolError` fallback remain the authoritative,
unchanged boundary. New unit tests over the real SDK `ListTools` surface plus integration
assertions; ~16 existing MCP test files repaired via a shared `whoamiHandler`/`newWhoamiBackend`
helper. Full unit + Docker + integration suites green, vet clean, review found zero findings.

## What Was Built

- **Spec** `docs/superpowers/specs/2026-06-25-mcp-discovery-role-filtering-design.md`.
- **Plan** `docs/plans/2026-06-25-mcp-discovery-role-filtering-plan.md`.
- **Fix** `internal/mcp/server.go` - `Server.isAdmin` field, startup whoami resolution in
  `NewServer`, conditional `registerReservations()` behind `if s.isAdmin`.
- **Tests** `internal/mcp/registration_test.go` (real `ListTools` discovery surface),
  integration assertions in `mcp_integration_test.go`, shared helper in
  `whoami_test_helper_test.go`; ~16 existing test files repaired to answer `/v1/users/me`.
- **Backlog** closed
  `docs/backlog/closed/bug-2026-05-09-mcp-admin-tools-role-filtering.md`.

## What Went Well

- **Filtering layered on top of the real boundary, not in place of it.** The change is purely
  cosmetic discovery hygiene; the spec was explicit that `auth(admin(...))` and the forbidden
  `ToolError` stay authoritative. That framing kept the threat model honest - a non-admin who
  guesses the tool name still gets a 403, so the filter is defense in depth and UX, never the
  gate.
- **Reused the existing identity path.** No new endpoint or client method - `callWhoami` already
  existed, so the fix is a startup call plus a comma-ok assertion, not new surface area.
- **Right-sized verification.** Unit tests over the actual SDK `ListTools` surface (not an
  internal registry mock) plus integration coverage for admin/non-admin and auth-expired, with
  a single adversarial review pass. Proportionate for a backend bug fix; review found nothing.

## Lesson: a startup network call in a constructor ripples across every test that builds the object

The one real cost this session: adding a `callWhoami` to `NewServer` turned construction into a
network operation, so **every test that builds a `Server` now needs its httptest backend to
answer `GET /v1/users/me`** - about 16 files. The plan anticipated most of this and prescribed
a shared `whoamiHandler`/`newWhoamiBackend` helper up front, which was the right call. But the
engineer still found 3-4 construction sites the plan's enumeration missed: the `whoami_test.go`
AuthExpired call-counter, the `delivery_test` redirect handler, the `resources_test` ServerInfo
handlers, and the integration AuthExpired path.

Two takeaways for next time:

1. **When a constructor gains a side effect (network, disk, clock), assume the blast radius is
   "every test that constructs the type," and design the shared test helper before writing
   production code** - not as cleanup after the compiler complains. The helper here was correct;
   it just needed to land first and be applied exhaustively.
2. **The planner should enumerate ALL construction sites, not most.** `grep` for the constructor
   (`NewServer(`) across the package's `_test.go` files is a cheap, exhaustive way to get the
   complete list; "most of them via a helper" leaves the engineer rediscovering the tail by
   compile error. An exhaustive call-site list in the plan would have closed the 3-4-site gap.

## Follow-on / Remaining Surface

`relay_list_reservations` (GET /v1/reservations) is genuinely the **only** admin-only MCP tool.
Audited the full `auth(admin(...))` route set in `internal/api/server.go` against the registered
MCP tools:

- The MCP `workers` tool only calls GET `/v1/workers` and GET `/v1/workers/{id}`, both
  global-auth reads - not the admin PATCH/disable/enable/token routes.
- The MCP `schedules`/`schedules_write` tools hit `/v1/scheduled-jobs*`, which are owner-or-admin
  (already correctly left registered for everyone; `run_now` is owner-or-admin and stays
  visible).
- The rest of the admin-only API surface (invites, agent-enrollments, worker
  disable/enable/token, revoked workers, user management, worker workspaces) has **no MCP tool
  at all**, so there is nothing to discovery-filter there today.

If a future MCP tool is added over any of those admin endpoints, it must follow the same pattern
(register behind `if s.isAdmin`). No new backlog item is warranted now - there is no second
admin-only tool to filter.
