---
date: 2026-06-20
topic: job-status-recompute-race
branch: claude/strange-nobel-08bd7a
pr: 36
merge: cda9a3a
---

# Session Retro: 2026-06-20 - Job Status Recompute Race Fix

**TL;DR:** Closed `bug-2026-06-10-job-status-recompute-race` by replacing the worker handler's read-modify-write job-status recompute with a single atomic `RecomputeJobStatus :one` UPDATE, so the last writer always sees current task state and a job can no longer wedge in `running` with all tasks done. The headline process lesson: the first draft of the concurrency regression test pre-set both tasks to `done`, so it passed against the buggy code and proved nothing - the integration tester caught it in Phase 4 and rewrote it to race two goroutines so it genuinely reproduces the interleaving.

## What Was Built

A job whose last two tasks finish on two different agents concurrently can no longer strand itself in `running` forever (terminal SSE `job` event never firing, `overlap_policy="skip"` schedules wedging because `CountActiveJobsForSchedule` keeps counting the done job as active).

- **Atomic recompute query** (`internal/store/query/jobs.sql`) - new `RecomputeJobStatus :one`: a single `UPDATE jobs ... FROM (SELECT aggregate over tasks)` so the status decision and the write happen in one statement. The previous read-modify-write (list tasks -> compute -> write) let a stale `running` write land last when two completions interleaved.
- **Thin-wrapper rewrite** (`internal/worker/handler.go`) - `updateJobStatusFromTasks` now just calls the atomic query, preserving its existing signature and its `""`-on-error contract so the terminal SSE `job` event still fires on the happy path.
- **Dead-copy deletion** (`internal/api/jobs.go`) - removed the orphaned zero-caller copy of `updateJobStatusFromTasks` flagged in the backlog item.
- **Store-layer integration test** (`TestRecomputeJobStatus`) - covers the status-aggregation cases plus a concurrent-completion sub-test that claims both tasks then races a mark-done goroutine against a recompute goroutine.

## Key Decisions

- **Pushed the fix down to the store, not the handler.** The interleaving is a read-modify-write hazard; the only durable fix is to collapse read and write into one SQL statement. Locking or serializing in Go would have been a weaker, more complex guard that still races across server processes.
- **Preserved the wrapper's signature and error contract instead of inlining the query at the call site.** Keeping `updateJobStatusFromTasks` as a thin wrapper meant the terminal SSE event path and its `""`-on-error behavior stayed byte-for-byte the same - a surgical change that the caller could not tell apart except under the race.

## Problems Encountered

- **The first concurrency regression test would have passed against the buggy code.** The initial sub-test pre-set both tasks to `done` before recomputing, so there was no interleaving to expose - it had zero regression value. Phase 4 rewrote it to claim both tasks and then race two goroutines (one marking done, one recomputing), which actually reproduces the stale-write window. A "concurrency regression test" that does not fail against the pre-fix code is not a regression test; it is a coverage line.
- **The harness blocked self-merge to main on an unattended run.** The merge step required a `Bash(gh pr merge:*)` permission rule that did not exist, so the autopilot run stalled at integration until the user granted it. Worth pre-granting (or routing merge through the conductor) for future unattended runs so Phase 5 does not block on a permission prompt.

## Improvement Goals

- **A regression test must provably fail against the unfixed code before it counts as the proof.** Construct the failing interleaving (or timing) first, observe red, then fix - a test built on a fixture state the bug cannot reach (here, both tasks pre-set to `done`) silently certifies nothing. Sharpens the carried "red test must distinguish red from green" lesson from the 2026-06-19 pipe-drain retro into the concurrency case.
- **Pre-grant or delegate the merge permission for unattended runs.** An autopilot iteration that can build, test, and review but cannot complete `gh pr merge` stalls at the finish line. Decide up front whether the agent self-merges (needs the permission rule) or the conductor owns integration.

## Files Most Touched

- `internal/store/query/jobs.sql` - new `RecomputeJobStatus :one` atomic UPDATE.
- `internal/worker/handler.go` - `updateJobStatusFromTasks` rewritten as a thin wrapper over the atomic query.
- `internal/api/jobs.go` - deleted the dead zero-caller copy of `updateJobStatusFromTasks`.
- store-layer integration test - `TestRecomputeJobStatus`, including the goroutine-racing concurrent-completion sub-test.
