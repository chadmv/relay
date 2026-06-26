---
title: relay_wait_for_job poll interval too coarse for sub-2s jobs
type: bug
status: closed
created: 2026-05-09
closed: 2026-06-25
resolution: fixed
source: MCP server session retro
---

# relay_wait_for_job poll interval too coarse for sub-2s jobs

## Summary
relay_wait_for_job polls every 2 s. For sub-2 s tasks the caller always waits at least one full poll interval. A future version could drop to 500 ms or use Postgres LISTEN/NOTIFY.

## Proposal
Drop the default poll interval to 500 ms, or wire the wait loop to Postgres LISTEN/NOTIFY on the `relay_task_completed` channel so the tool returns as soon as the job transitions to a terminal state.

## Acceptance / Done When
- Fast jobs (< 500 ms) are observed to return within ~500 ms of completion in practice.
- Or: LISTEN/NOTIFY path is wired and the poll fallback is only used as a safety net.

## Related
- `internal/mcp/wait.go` — `waitPoll` constant (currently 2 s)
- `internal/store/query/tasks.sql` — `NotifyTaskCompleted` query

## Notes
- The `waitPoll` constant in `internal/mcp/wait.go` is already injectable in tests
  (overridable package var), so lowering the default is a one-line change. The open
  question is whether 500 ms improves typical workloads without creating excess API
  load; if LISTEN/NOTIFY is wired, the poll interval matters less and becomes a fallback.
- Merged [[idea-2026-05-09-relay-wait-job-shorter-poll]] here (closed as duplicate
  2026-06-18); that item asked the same "shorter default poll for fast jobs" question.

## Resolution
Fixed 2026-06-25 via Option A (adaptive client-side poll schedule). `relay_wait_for_job`
(`internal/mcp/wait.go`) now drives its inter-poll sleep from a pure `nextWaitInterval(attempt)`
helper: 500ms for the first 4 attempts (covering the first ~2s where fast completion is likely),
then the existing 2s steady cadence for longer waits. So sub-2s jobs return within ~500ms of
completion while long-job GET load stays within ~10% of before. The first GET is immediate; the
deadline clamp, ctx-cancel, and terminal-state behavior are unchanged. The existing `s.waitPoll`
test override is preserved as a flat-interval bypass. LISTEN/NOTIFY (the deeper alternative) was
ruled out of scope: the MCP server is an HTTP client holding only a `relayclient.Client` with no
Postgres connection, so `NotifyTaskCompleted` is unreachable. An auth'd SSE endpoint
(`GET /v1/events?job_id=`) does exist and is a feasible future enhancement, but still needs a
terminal-check-after-subscribe plus a poll fallback for a sub-second marginal win, so it was filed
separately rather than blocking this fix. Pure client-side change; no Invariant touched. Unit
(incl. a wall-clock-bounded regression test proven non-vacuous) + integration green on
Windows/Docker; `go vet` clean; review found no high/medium issues.
