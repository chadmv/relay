---
title: relay_wait_for_job shorter poll interval for fast jobs
type: idea
status: closed
created: 2026-05-09
source: MCP server session retro
closed: 2026-06-18
resolution: duplicate
---

# relay_wait_for_job shorter poll interval for fast jobs

## Summary
Should `relay_wait_for_job` have a shorter default poll interval (e.g. 500 ms) for better perceived responsiveness on fast jobs? The current 2 s interval feels slow when a task completes in under a second.

## Notes
The `waitPoll` constant in `internal/mcp/wait.go` is already injectable in tests (overridable package var), so changing the default is a one-line change. The real question is whether 500 ms is better for typical workloads without creating excess API load. If LISTEN/NOTIFY is wired (see related bug), polling becomes a fallback only and the interval matters less.

## Related
- `internal/mcp/wait.go` — `waitPoll` constant
- `bug-2026-05-09-wait-for-job-poll-interval` — related bug about coarse polling; LISTEN/NOTIFY is the deeper fix

## Resolution
Duplicate of [[bug-2026-05-09-wait-for-job-poll-interval]]. Both ask for a shorter default
poll for sub-2 s jobs, with LISTEN/NOTIFY as the deeper fix; this idea is a strict subset.
The unique detail (waitPoll is already test-injectable, so the default is a one-line change;
the 500 ms-vs-excess-API-load tradeoff) was folded into that bug's Notes before closing.
Merged during the 2026-06-18 /roadmap deep review.
