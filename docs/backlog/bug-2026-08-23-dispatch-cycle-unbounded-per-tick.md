---
title: The dispatch cycle has no LIMIT and rebuilds per-task state inside the loop
type: bug
status: open
created: 2026-08-23
priority: medium
source: 2026-08-23 deep roadmap refresh - gaps agent finding
---

# The dispatch cycle has no LIMIT and rebuilds per-task state inside the loop

## Summary
`GetEligibleTasks` has no LIMIT (`internal/store/query/tasks.sql:187-197`), so every eligible
pending task loads into memory on every cycle (the 30s ticker plus every `Trigger()`), and
`selectWorker` rebuilds the `reservedIDs` map and re-unmarshals the task's source JSON per task
(`internal/scheduler/dispatch.go:70-159`, `:184-203`) - O(tasks x (workers + reservations)) work
per tick. At render-farm queue depths (tens of thousands of queued frames) each dispatch pass
becomes a multi-second stall on the single dispatcher goroutine, which also serves the
requeue-on-send-failure path.

## Context
Relay's stated domain is render-farm orchestration, where deep queues are normal, not
pathological. Nothing is broken at current test scale; this is a scalability defect filed before
the first incident rather than after. Found by the 2026-08-23 gaps pass; no existing item covered
dispatch batching or cycle cost.

## Proposal
Bound the scan (a LIMIT well above worker-slot capacity, e.g. proportional to total free slots)
and hoist the loop-invariant state (reservation set, per-worker free counts) out of the per-task
path. Two traps to record: a LIMIT interacts with ordering - once priority-aware dispatch lands
([[feature-2026-08-23-dispatcher-never-reads-job-priority]]), the LIMIT must apply after the
priority ordering or high-priority work beyond the window starves; and the watchdog slice's
`WatchdogMaxRowsPerSweep` is the in-repo precedent for a bounded scan with a stated re-entry
guarantee.

## Acceptance / Done When
- The eligible-task scan is bounded, with the bound's derivation stated in the query comment.
- The reservation set and any other loop-invariant state is built once per cycle, not per task.
- A test pins that tasks beyond the scan window are picked up by subsequent cycles (no silent
  starvation from the LIMIT).

## Related
- `internal/store/query/tasks.sql:187-197`, `internal/scheduler/dispatch.go:70-203`
- [[feature-2026-08-23-dispatcher-never-reads-job-priority]] - ordering and LIMIT must be designed together
