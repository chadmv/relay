# Relay MCP Server — Design

**Date:** 2026-05-08
**Status:** Approved (pending implementation plan)

## Summary

Add a `relay mcp` subcommand to the existing CLI that runs a Model Context
Protocol (MCP) server over stdio. The server gives MCP clients (Claude Desktop,
Claude Code, etc.) read access and a curated set of safe write actions against
a relay deployment, authenticated as the user who logged in via `relay login`.
Capabilities are gated by the calling user's bearer token: non-admins see a
narrower surface; admin-only mutations return a structured `forbidden` error.

## Goals

- Let an operator or admin manage their relay deployment conversationally
  ("show me failing jobs from today", "submit this render", "cancel job X").
- Let an agentic client construct a job spec from a high-level goal, submit it,
  wait for completion, and report results.
- Respect existing relay permissions — one MCP server, capabilities gated by
  the user's token. Admins get more; non-admins get the same surface they
  already have via the CLI.
- Ship inside the existing `relay` binary so no new install step is required.

## Non-goals (v1)

- Multi-user / remote MCP transport (HTTP+SSE hosted by `relay-server`).
- Destructive admin mutations (worker token revoke, evict-workspace, agent
  enrollment, invite creation, all user mutations, force-cancel, password
  ops). These are deferred to a v2 pass with deliberate confirmation UX.
- MCP **prompts**.
- Caching of any kind.
- Resource subscriptions or change notifications.
- Reservation create/delete (list-only in v1).

## User Flow

1. User runs `relay login` once to populate `~/.relay/config.json`.
2. User adds an entry to their MCP client config:
   ```json
   { "mcpServers": { "relay": { "command": "relay", "args": ["mcp"] } } }
   ```
3. The client launches `relay mcp` on stdio. The server reads the saved token,
   verifies it by calling `GET /v1/users/me`-equivalent (whoami), and registers
   tools and resources. If no token is present or the token is expired, the
   server logs a clear stderr message and exits non-zero; the client surfaces a
   connection failure.
4. The user interacts in their MCP client. Each tool call hits the relay REST
   API as the logged-in user.

## Architecture

### Deployment

- **Subcommand of the existing CLI** — `relay mcp`. No new binary, no new
  install step. Distributed and versioned alongside `relay-server` and
  `relay-agent`.
- **Stdio transport.** The MCP client owns the process lifetime.
- **Pure REST client.** The MCP server is a client of relay's existing HTTP
  API (`:8080`). No new gRPC or database connections.

### Code layout

- `cmd/relay/main.go` — register `mcp` in the existing dispatcher.
- `internal/cli/mcp.go` — command wiring: parse flags, load config, build the
  MCP server, run on stdio.
- `internal/mcp/` — new package containing tool registrations, resource
  handlers, and JSON marshaling. Imports `internal/cli/client` (extracted
  below) for HTTP calls.
- `internal/cli/client/` — **new sub-package** factored out of `internal/cli`.
  Contains the HTTP helpers (request/response, envelope parsing, typed
  result types) currently entangled with the `flag`-based command surface.
  All existing CLI commands re-route through this package; existing CLI tests
  continue to pass unchanged.

### Server-side additions

One small additive REST change is required:

- **Paginated task-log query.** `GET /v1/tasks/{id}/logs?since_seq=&limit=`
  with response envelope `{items, next_seq, total}`. The endpoint exists
  today but its current shape needs verification; if it does not already
  accept `since_seq` and `limit`, the implementation plan's first MCP task
  is to add them. No breaking changes to existing callers.

### SDK

`github.com/modelcontextprotocol/go-sdk` (the official SDK). One new direct
dependency.

## Tool Surface (v1)

All tools are namespaced `relay_*`. Tools that map to admin-only relay
endpoints are registered for all users — when called by a non-admin they
return a structured `forbidden` error so Claude can explain the failure to the
user instead of pretending the action does not exist.

### Read tools

| Tool | Args | Returns |
|---|---|---|
| `relay_whoami` | _(none)_ | `{user_id, email, name, is_admin, server_url}` |
| `relay_list_jobs` | `{status?, limit?, cursor?}` | paginated job list |
| `relay_get_job` | `{job_id}` | job + summary stats |
| `relay_list_tasks` | `{job_id}` | tasks for a job |
| `relay_get_task` | `{task_id}` | single task |
| `relay_get_task_logs` | `{task_id, since_seq?, limit?}` | log lines |
| `relay_list_workers` | `{limit?, cursor?}` | paginated worker list |
| `relay_get_worker` | `{worker_id}` | single worker |
| `relay_list_schedules` | `{limit?, cursor?}` | own schedules; admins see all |
| `relay_get_schedule` | `{schedule_id}` | single schedule |
| `relay_list_reservations` | `{limit?, cursor?}` | admin-only at the API layer |

### Write tools (the "safe writes" tier)

| Tool | Args | Returns |
|---|---|---|
| `relay_submit_job` | `{job_spec}` | `{job_id, status}` |
| `relay_cancel_job` | `{job_id}` | `{status}` (no `force` in v1) |
| `relay_wait_for_job` | `{job_id, timeout_seconds?}` | final job state + last task-status summary |
| `relay_create_schedule` | `{name, cron_expr, timezone?, overlap_policy?, job_spec}` | created schedule |
| `relay_update_schedule` | `{schedule_id, cron_expr?, timezone?, overlap_policy?, enabled?}` | updated schedule |
| `relay_delete_schedule` | `{schedule_id}` | `{ok: true}` |
| `relay_run_schedule_now` | `{schedule_id}` | created job (admin-only at the API) |

`job_spec` is an inline JSON object matching the existing
`POST /v1/jobs` shape. It is validated client-side using the same
`JobSpec`/`ValidateJobSpec` logic from `internal/api/job_spec.go` (or a
shared subset reachable by both packages without an import cycle), so
malformed specs are rejected before hitting the network.

### `relay_wait_for_job` mechanics

- Polls `GET /v1/jobs/{id}` every 2 s until the job is in a terminal state
  (`done`, `failed`, `cancelled`) or `timeout_seconds` elapses.
- Default timeout: 60 s. Maximum: 300 s (hard cap to keep the tool call
  bounded).
- On timeout returns `{timed_out: true, last_state: ...}` so Claude can decide
  whether to call again.
- Deliberately does not use SSE — keeps the implementation trivial and
  avoids streaming inside an MCP tool call.

## Resources

Two resources, read-on-demand and re-fetched fresh on every read (no caching):

- **`relay://server-info`** — JSON: `{server_url, user, server_version,
  capabilities}`. Lets Claude orient at session start: who am I, what server,
  what tier of tools will succeed.
- **`relay://recent-jobs`** — JSON: the last 20 jobs the calling user can see
  (`relay_list_jobs` with `limit=20`), exposed as a resource so Claude can
  pull a quick "what's going on" view without spending a tool turn.

No resource templates (`relay://jobs/{id}`) in v1 — those overlap directly
with `relay_get_job` and add discovery ambiguity.

## Auth & Config

Resolution order (matches the existing CLI):

1. `RELAY_URL` / `RELAY_TOKEN` env vars (highest precedence).
2. `~/.relay/config.json` (Linux/macOS) or `%APPDATA%\relay\config.json`
   (Windows).
3. If neither is present, `relay mcp` exits non-zero with a stderr message
   ("not logged in — run `relay login` first").

The token is read once at startup. If it expires mid-session, every tool call
returns an `auth_expired` error and the user reconnects the MCP client.

## Errors

Every tool returns one of two shapes:

- **Success** — a JSON object specific to the tool.
- **Failure** — an MCP tool error with a structured payload
  `{code, message, hint?}` where `code` is one of:

| Code | Maps from | Hint pattern |
|---|---|---|
| `auth_expired` | 401 | "run `relay login` to refresh credentials" |
| `forbidden` | 403 | "this action requires an admin token" |
| `not_found` | 404 | "no such {entity}; check the id" |
| `validation` | 400 | tool-specific (e.g. "cron_expr must be 5-field or `@hourly`/`@daily`/`@every`") |
| `conflict` | 409 | tool-specific (e.g. "schedule with that name already exists") |
| `rate_limited` | 429 | "rate limit hit; wait and retry" |
| `server_error` | 5xx | "server error; check `relay-server` logs" |
| `network` | transport | "could not reach server at {url}" |

Mapping is centralised in `internal/mcp/errors.go`.

## Pagination

Tool args mirror the REST envelope:

- **In:** `{limit?, cursor?}` — `limit` defaults to 50, max 200 (matches
  REST). `cursor` is opaque.
- **Out:** `{items, next_cursor, total}`. `next_cursor: ""` means last page.

Claude calls again with the cursor to advance.

## Logging

MCP servers must not write to stdout (it is the protocol channel). All
diagnostics go to stderr. Standard Go `log` is configured to stderr at
startup. No structured logging in v1.

## Testing

### Unit tests — `internal/mcp/*_test.go`

For each tool: arg parsing, error mapping, response shaping. The relay HTTP
client is mocked via the same `httptest.Server` pattern the CLI tests already
use (`internal/cli/admin_users_test.go` is the model).

### Integration tests — `internal/mcp/mcp_integration_test.go` (`//go:build integration`)

Spin up a real `relay-server` via the existing testcontainers Postgres
fixture, then exercise the MCP server end-to-end via an in-process MCP client
from the Go SDK. Required cases:

- `whoami` returns the logged-in user.
- Submit a trivial job, wait for it, fetch logs.
- Pagination across `list_jobs` (seed >50 jobs).
- Admin-only call as non-admin → `forbidden` error.
- `cancel_job` happy path.
- Schedule create / update / delete round-trip.
- `auth_expired` path (revoke the token, then call a tool).

### Manual smoke

Wire the binary into Claude Desktop's config and run a short scripted session:
list jobs, submit `examples/hello-windows.json`, wait for it, read its logs.

## Documentation

Add a new top-level section to `README.md` titled **"MCP integration"** with:

- Claude Desktop and Claude Code config snippets.
- Prerequisite: run `relay login` first.
- Short tool-list reference (the table above, condensed).
- Note on capabilities-by-token.

No separate doc file.

## Risks & Mitigations

- **`internal/cli/client` extraction.** The CLI's HTTP helpers are entangled
  with `flag` parsing and stdout printing. *Mitigation:* the implementation
  plan's first task is the extraction, with all existing CLI tests passing
  before any MCP code is written.
- **Task-logs endpoint shape.** The plan's first MCP-side task is "verify
  `GET /v1/tasks/{id}/logs` shape; add `since_seq`/`limit` params if
  missing." Small additive REST change; confirmable in well under an hour.
- **`relay_wait_for_job` timing.** 2 s poll is fine for typical jobs but
  feels slow when a task finishes in <2 s. Accepted in v1; if it is
  annoying in practice, drop to 500 ms or wire LISTEN/NOTIFY later.
- **Token lifetime.** 30-day tokens make mid-session expiry rare, but the
  `auth_expired` error path needs to be exercised in tests (covered
  above).
- **Job-spec validator sharing.** Importing `internal/api` from
  `internal/mcp` would pull in HTTP server code. *Mitigation:* either
  factor `JobSpec`/`ValidateJobSpec` into a leaf package (e.g.
  `internal/jobspec`) consumed by both, or duplicate the small validator.
  Decision deferred to the implementation plan, which will pick whichever
  is smaller.

## Open Questions

None blocking. The two implementation-time decisions (task-logs endpoint
shape; jobspec validator sharing) are small, additive, and resolvable
inside the plan.
