---
title: relay_wait_for_job poll interval too coarse for sub-2s jobs
type: bug
status: open
created: 2026-05-09
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
