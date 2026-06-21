---
date: 2026-06-20
topic: dispatch-failure-paths-silent
branch: claude/blissful-brown-c7780a
pr: "2026-06-20 / dispatch-failure-paths-silent"
merge: "2026-06-20 / dispatch-failure-paths-silent"
---

# Session Retro: 2026-06-20 - Dispatch failure paths are inconsistent and silent

**TL;DR:** Closed `bug-2026-06-10-dispatch-failure-paths-silent`. Three dispatch-loop
gaps fixed: (1) bad persisted `source` JSON no longer leaks a dispatched slot, (2) bad
`commands` JSON no longer causes infinite claim/requeue churn with unbounded
`assignment_epoch` growth, and (3) DB errors in `dispatch` and `handleTaskStatus` are no
longer swallowed silently. Both bad-JSON cases now terminally fail the claimed task
through a shared `failClaimedTask` helper.

## What Was Built

- `internal/scheduler/dispatch.go`: a private `failClaimedTask` helper that marks an
  already-claimed task `failed` via the epoch-fenced `UpdateTaskStatus` at the claim's
  own non-zero epoch (not bumped - `failed` is terminal, so the assignment ends),
  cascades `FailDependentTasks`, recomputes job status, and publishes `task` + `job` SSE
  events. Both the bad-`source` and bad-`commands` branches now call it instead of
  leaking the slot / requeueing.
- Error logging on every DB error path in `dispatch` and `handleTaskStatus`
  (`GetEligibleTasks`, `ListWorkers`, `ListActiveReservations`,
  `CountActiveTasksByAllWorkers`, `GetTask`, `IncrementTaskRetryCount`,
  `UpdateTaskStatus`, `FailDependentTasks`, `RecomputeJobStatus`). The benign
  `ClaimTaskForWorker` `pgx.ErrNoRows` claim race stays silent.
- New scheduler tests: no-requeue / epoch-stable on bad commands, no-slot-leak on bad
  source, and a terminal `job` SSE event on the fail path.

## Key Decisions

- **Epoch fence:** no new "mark single task failed" store query was added. The fix reuses
  the existing epoch-fenced `UpdateTaskStatus` at the claim's real epoch and does not bump
  it (the assignment ends on terminal failure), satisfying the invariant without a
  parallel task-failure path.
- **D2 (logging the claim race):** narrowed the backlog proposal's "log every error path"
  to skip the benign `pgx.ErrNoRows` claim race - logging it would be misleading noise on
  the happy path.
- **Verification finding routed back:** `failClaimedTask` initially mirrored
  `handleTaskStatus` for the `task` event but dropped the `job` event on terminal flip.
  Added it so the dispatcher and agent terminal paths emit the same event set (SPA
  job-level subscribers now update).

## Backlog Triage

- None. The two double-parse / log-quoting smells the reviewer flagged were rated low and
  explicitly "not worth changing for this diff" / "no code change required"; left as-is
  per the surgical-changes rule. No new items filed.
