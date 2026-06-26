# MCP Discovery-Time Role Filtering for Admin-Only Tools

- Date: 2026-06-25
- Status: Design (autonomous, no human gate)
- Backlog item: `docs/backlog/bug-2026-05-09-mcp-admin-tools-role-filtering.md`
- Owner: relay-tpm

## Problem

The relay MCP server registers every tool unconditionally for every session. The
only admin-only tool today, `relay_list_reservations`, is therefore advertised in
the MCP tool list to non-admin users. A non-admin discovers they lack access only
by calling the tool and receiving a `forbidden` ToolError from the server-side
`AdminOnly` check. This is confusing: the tool appears usable but is not.

We want discovery-time filtering: a non-admin session should not see admin-only
tools in its tool list at all. The server-side authorization stays authoritative;
filtering at registration is a usability layer, not a security boundary.

## Current behavior (grounded in code)

- `NewServer(serverURL, token)` (`internal/mcp/server.go`) is fully synchronous
  and does no network I/O. It constructs the relay client, then calls
  `s.registerTools()` and `s.registerResources()`, which register all tools
  including `registerReservations()`.
- `is_admin` is NOT known at `registerTools` time today. There is no startup
  identity resolution. `whoami` is itself a lazily-invoked MCP tool:
  `registerWhoami()` adds the tool, and `callWhoami(ctx)` performs
  `GET /v1/users/me` only when the tool is called. `callWhoami` already returns
  `is_admin` from that response, so the data source exists; it just is not
  consulted during construction.
- `relay_list_reservations` (`internal/mcp/reservations.go`) is the single
  admin-only tool. Its description says "(admin-only)"; on a non-admin call the
  backend returns 403, which `MapError` turns into a `forbidden` ToolError
  (verified by `TestListReservations_Forbidden`).
- `relay_run_schedule_now` (`internal/mcp/run_now.go`) is owner-or-admin, not
  admin-only (reconciled in `bug-2026-06-18-run-now-not-admin-gated`). It is
  correctly available to non-admins and is OUT OF SCOPE here.
- All other registered tools (`whoami`, `jobs`, `tasks`, `task_logs`, `workers`,
  `schedules` read/write, `submit`, `cancel`, `wait`, `run_now`) are not
  admin-only and stay registered for everyone.
- The MCP server is launched from `internal/cli/mcp.go`: `NewServer(...)` then
  `srv.Run(ctx, os.Stdin, os.Stdout)`. A `NewServer` error is returned up and the
  command exits.

## Chosen approach

Resolve the caller's identity once at startup and register the admin-only tool
only when `is_admin == true`. The forbidden error path stays as a
belt-and-suspenders fallback.

Concretely:

1. During `NewServer`, after constructing the client, fetch the caller identity
   by calling the existing `callWhoami` (or an internal equivalent that returns
   the `is_admin` bool). This is one `GET /v1/users/me` at startup.
2. Split tool registration into two phases:
   - Unconditional tools (everything registered today except reservations) are
     always registered.
   - `registerReservations()` is called only when `is_admin == true`.
3. Keep the existing `forbidden` ToolError path in `callListReservations`
   unchanged. It remains the authoritative server-side enforcement and a fallback
   if an admin is demoted mid-session or registration logic regresses.

Rationale for resolving identity in `NewServer` rather than threading a flag from
the CLI: `callWhoami` already lives in the mcp package and already returns
`is_admin`; the CLI does not currently know the admin flag (it holds only
`ServerURL` and `Token`). Resolving inside `NewServer` keeps the knowledge local
to the package that owns tool registration and avoids adding an auth round-trip in
the CLI layer.

### Whoami-failure behavior at startup (edge case)

If the startup whoami call fails (network error, expired token, server down),
`NewServer` must surface that failure in a way that exits cleanly, matching today's
behavior where a bad token simply fails the command. The decision:

- `NewServer` returns the whoami error to the caller. `internal/cli/mcp.go`
  already returns any `NewServer` error up, so the command exits with a non-zero
  status and a message. No partial/started server, no panic.
- We do NOT fall back to "register admin tools anyway" and we do NOT fall back to
  "register only non-admin tools and continue". Both would silently degrade. A
  failed identity check at startup is a hard error, consistent with the existing
  "not logged in" failure mode in `mcp.go`.

This means `NewServer` gains a network dependency it did not have before. That is
acceptable: the server is useless without a reachable, authenticated backend, and
the CLI already treats `NewServer` failure as fatal. Tests that call
`NewServer(url, token)` against an `httptest.Server` must now ensure that server
answers `GET /v1/users/me` (see Test impact).

## Scope

In scope:
- Startup identity resolution in the mcp package.
- Conditional registration of `relay_list_reservations`.
- Updating existing mcp tests that construct `NewServer` so the test backend
  serves `/v1/users/me`.

Out of scope:
- `relay_run_schedule_now` (owner-or-admin, not admin-only).
- Any change to server-side `AdminOnly` enforcement or the `forbidden` ToolError.
- MCP resources (`relay://server-info`, `relay://recent-jobs`) - neither is
  admin-only; they stay unconditional.
- Re-checking admin status mid-session or re-registering tools on role change.
  The MCP session is short-lived per process; the server-side check covers a
  mid-session demotion.

## Acceptance criteria

1. Non-admin MCP session: `relay_list_reservations` is absent from the tool list.
2. Admin MCP session: `relay_list_reservations` is present and functional
   (returns reservations as today).
3. Whoami failure at startup (unreachable backend, 401/expired token): `NewServer`
   returns an error and the `relay mcp` command exits cleanly with that error.
   No server is served. This matches existing behavior for the "not logged in"
   and bad-token cases.
4. All non-admin tools remain registered for both admin and non-admin sessions.
5. The server-side `forbidden` path on `callListReservations` is unchanged and
   still covered by its existing test.

## Test impact and harness considerations

How MCP tools are tested today (verified):
- Unit tests per tool (e.g. `reservations_test.go`, `whoami_test.go`,
  `server_test.go`) construct `NewServer(url, token)` against an
  `httptest.Server` and then call the tool's `callXxx` method directly. They do
  not exercise the MCP tool-list/registration surface.
- `server_test.go` has `TestNewServer_ValidCredentials` which calls
  `NewServer("http://localhost:8080", "tok")` with no backend - this will break
  once `NewServer` makes a startup whoami call. It must be updated to point at an
  `httptest.Server` that serves `/v1/users/me`, or split so the
  offline-validation cases (empty url/token) stay offline while the
  valid-credentials case gets a live test backend.
- Integration tests (`mcp_integration_test.go`) already spin up a real relay
  server via `startRelayForMCP` and seed admin/non-admin users with
  `seedAndLogin(..., isAdmin)`. These are the right place to assert discovery-time
  filtering against a real `/v1/users/me`.

New/updated tests to add:
- Unit: admin whoami response -> `relay_list_reservations` registered; non-admin
  whoami response -> not registered. This needs a way to assert which tools are
  registered. Two options for the implementer (pick one in the plan):
  (a) inspect the MCP SDK server's registered-tools list if the SDK exposes it,
  or (b) add a small internal test accessor in the mcp package that reports
  registered tool names. Prefer the SDK's own list-tools surface if available so
  the test exercises the real discovery path; fall back to an internal accessor
  only if the SDK does not expose registered tools.
- Unit: startup whoami failure (test backend returns 401 or is unreachable) ->
  `NewServer` returns an error.
- Update every existing `NewServer(url, token)` call site in mcp tests whose
  backend does not already answer `/v1/users/me` so construction succeeds. The
  per-tool `httptest` handlers currently assert a single expected path; they will
  need to also handle the startup `/v1/users/me` probe (e.g. branch on
  `r.URL.Path`).
- Integration: non-admin session does not list `relay_list_reservations`; admin
  session does and can call it.

## Invariants

- Single job-spec pipeline: not touched. This change is read-side tool
  registration only; no job-spec ingestion path is added or altered.
- Auth authority stays server-side: discovery filtering is purely cosmetic. The
  `AdminOnly` middleware and the `forbidden` ToolError remain the real
  enforcement. A client that hand-crafts a `relay_list_reservations` call despite
  it being unlisted still gets a 403. The spec explicitly keeps that path.
- Single JSON entry point / one bounded sender / epoch fence / identity-checked
  teardown / no interior pointers across locks: none are in this change's path
  (no HTTP request decoding on the server, no gRPC stream, no task-status writes,
  no shared registry mutation). The only new behavior is one client-side
  `GET /v1/users/me` at MCP startup.

## Open questions

None blocking. The one implementer decision deferred to the plan is the test
mechanism for asserting which tools are registered (SDK list-tools vs. internal
accessor); the spec states the preference (SDK surface first).
