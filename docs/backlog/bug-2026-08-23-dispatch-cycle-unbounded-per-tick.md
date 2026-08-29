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

## Measured, and the axis the original filing missed (2026-08-29)

The security lens of the count-bounds Phase 4 review measured this item and found the per-task
re-parsing is worse than "source JSON" - **`internal/scheduler/labels.go` unmarshals BOTH `requires`
and `labels`, and it is called from inside `selectWorker`'s per-WORKER loop**, so the cost is
O(pending tasks x workers) per tick rather than O(pending tasks). `task.Source` is separately
unmarshalled three times per task (the eligibility check, the reservation arm, and
`taskIsSourceBearing`).

Measured at ~57-62 MB/s for a `map[string]string` unmarshal, from ONE 1 MiB request of 5000 tasks
carrying an 8-entry `requires` - a request entirely inside all three bounds that landed 2026-08-29:

| workers | unmarshals per tick | dispatcher CPU per tick |
|---|---|---|
| 10 | 50,000 | 0.13 s |
| 100 | **500,000** | **1.30 s** |

The dispatcher is a single goroutine, so that is a stall on all dispatch, not background load.

**The part that makes this self-sustaining, and the reason it outranks the rest of the item: a
`requires` map matching no worker's labels never dispatches.** The task stays `pending` permanently
and is re-read and re-parsed on every tick and every `Trigger()` for the life of the database. The
attacker also controls the tick rate, since `Trigger()` fires on every submission and every status
update.

**The 2026-08-29 count bounds do not fix this and were never going to.** `maxTasksPerJob = 5000` cuts
the many-tasks shape by roughly 3x and caps nothing in the fat-`requires` shape (5 tasks x 200 KB is
1 MB of parsing per worker per tick), because `Requires` and `Labels` are unbounded maps on an axis no
count bound covers.

**Hoisting the parse is a pure refactor with no new refusal**, and therefore none of the retroactivity
cost a validation bound would carry - decode `task.Requires` once per task before the worker loop and
pass the map in. That is the cheap half and it is worth doing on its own; the `LIMIT` is the other
half.

## Related
- `internal/store/query/tasks.sql:187-197`, `internal/scheduler/dispatch.go:70-203`
- [[feature-2026-08-23-dispatcher-never-reads-job-priority]] - ordering and LIMIT must be designed together
