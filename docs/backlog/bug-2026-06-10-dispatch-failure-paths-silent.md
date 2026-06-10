---
title: Dispatch failure paths are inconsistent and silent
type: bug
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

# Dispatch failure paths are inconsistent and silent

## Summary
Three observability/correctness gaps in the dispatch loop and task-status handling. (1) Bad persisted `source` JSON after a claim returns false without requeueing: the task stays `dispatched` against a worker that never received it, consuming a slot until that worker disconnects. (2) Bad `commands` JSON requeues, so the next cycle re-claims and fails again: infinite claim/requeue churn every trigger with `assignment_epoch` incrementing unboundedly. Both inputs are persistent data, so retrying can never succeed. (3) `dispatch` and `handleTaskStatus` swallow every DB error silently (`GetEligibleTasks`, `ListWorkers`, `GetTask`, `UpdateTaskStatus`, ...); a persistent DB issue makes dispatch stop entirely with zero log output, and a lost `done` update is invisible.

## Proposal
- Mark the task `failed` (and run `FailDependentTasks`) for both bad-JSON cases instead of dropping or requeueing.
- `log.Printf` on every error path in `dispatch` and `handleTaskStatus`; these loops are low-frequency, so log volume is not a concern.

## Related
- `internal/scheduler/dispatch.go:68-91, 228-253`
- `internal/worker/handler.go:408-497` (`handleTaskStatus`)
