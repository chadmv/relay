---
title: relay_wait_for_job shorter poll interval for fast jobs
type: idea
status: open
created: 2026-05-09
source: MCP server session retro
---

# relay_wait_for_job shorter poll interval for fast jobs

## Summary
Should `relay_wait_for_job` have a shorter default poll interval (e.g. 500 ms) for better perceived responsiveness on fast jobs? The current 2 s interval feels slow when a task completes in under a second.

## Notes
The `waitPoll` constant in `internal/mcp/wait.go` is already injectable in tests (overridable package var), so changing the default is a one-line change. The real question is whether 500 ms is better for typical workloads without creating excess API load. If LISTEN/NOTIFY is wired (see related bug), polling becomes a fallback only and the interval matters less.

## Related
- `internal/mcp/wait.go` — `waitPoll` constant
- `bug-2026-05-09-wait-for-job-poll-interval` — related bug about coarse polling; LISTEN/NOTIFY is the deeper fix
