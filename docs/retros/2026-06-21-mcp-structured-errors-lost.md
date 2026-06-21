---
date: 2026-06-21
topic: mcp-structured-errors-lost
branch: claude/gifted-meninsky-5fc18a
pr: "2026-06-21 / mcp-structured-errors-lost"
merge: "2026-06-21 / mcp-structured-errors-lost"
---

# Session Retro: 2026-06-21 - MCP structured errors lost

**TL;DR:** Closed `bug-2026-06-10-mcp-structured-errors-lost`. Every MCP tool returned `*ToolError`
as the Go error; go-sdk v1.6.0 flattens a returned error via `CallToolResult.SetError` to
`Code + ": " + Message` text, so the `Hint` field and JSON structure never reached clients. Added a
generic `addTool` wrapper that returns an `IsError` result carrying the marshaled `ToolError`
instead, and migrated all 18 registration sites to it. Autopilot iteration 6 (batch 2, item 2 of 4).

## What Was Built

- `internal/mcp/server.go` - generic `addTool[A, R](s, tool, call func(ctx, A) (R, *ToolError))`
  wrapper: on `*ToolError` returns `CallToolResult{IsError: true, Content: json(terr)}` (never the
  Go error, so the SDK can't flatten it); on success marshals the result exactly as before.
- All 18 tool registrations across 12 files (jobs, tasks, task_logs, workers, schedules read/write,
  reservations, submit, cancel, wait, run_now, whoami) migrated to the wrapper, removing the
  copy-pasted closures and each file's now-unused `encoding/json` import. whoami's no-args call uses
  a one-line `struct{}` adapter.
- `internal/mcp/delivery_test.go` - two delivery tests over the in-memory MCP transport: a 401 case
  (asserts `IsError` + the delivered text unmarshals to `code: auth_expired` with the `relay login`
  hint), proven RED before the fix, and a validation case (empty `job_id`, backend never hit).

## Key Decisions

- **One wrapper, not 18 inline fixes.** The bug was uniform across all 18 sites, and the proposal
  itself called for a shared wrapper. A generic `addTool` fixes the delivery centrally AND removes
  the duplication - the DRY win and the bug fix are the same change. The planner verified all 18
  success paths were byte-identical (single `TextContent` of `json(out)`) before committing to the
  uniform wrapper, so no site's output silently changed.
- **Delivery test over the real transport.** The existing `errors_test.go` asserted hints are SET;
  the gap was that nothing asserted they are DELIVERED. The new tests drive a registered tool through
  `NewInMemoryTransports` + `CallTool`, so they exercise the exact SDK path that was flattening the
  error - the only way to catch this class. Proven RED by the flattened `"auth_expired: ..."` text
  failing to unmarshal.

## Process Note

- The plan's task list omitted `task_logs.go` (site #6) even though its own survey table included it;
  the backend engineer caught the inconsistency and migrated it anyway, keeping the fix complete
  across all 18 sites. Good completeness instinct - a partial migration would have left site #6 still
  flattening errors. The conductor's own grep (`return nil, nil, terr` -> none) confirmed completeness
  before review.
- Worktree-path discipline (the iteration-4 lesson) was baked into the plan's command blocks and the
  engineer dispatch; all commits landed on the correct branch.
- Verification was a focused code-review rather than the relay-verify fan-out: the change is
  MCP-registration-layer only with in-memory unit coverage, so the Postgres/p4d integration tester
  adds nothing. The reviewer empirically re-confirmed the tests RED by reverting the wrapper.

## Backlog Triage

- No new items. The reviewer's one non-blocking observation (the wrapper discards `json.Marshal`
  errors, identical to the pre-fix code and unreachable for the all-string `ToolError`) did not
  warrant an item.
