---
title: handleCancelJob reads the job row without FOR UPDATE (cancel not fully serializable)
type: bug
status: open
created: 2026-07-01
priority: low
source: ROADMAP deep-refresh gaps sweep (2026-06-26)
---

# handleCancelJob reads the job row without FOR UPDATE (cancel not fully serializable)

## Summary
`handleCancelJob` reads the job with a plain `GetJob` (no `FOR UPDATE`), checks whether it is already
terminal, then calls `CancelJobTasks` + `UpdateJobStatus`. The task side of the race is defended by the
epoch bump inside `CancelJobTasks`, but the job-row read itself is unlocked, so two concurrent cancels
(or a cancel racing the agent's terminal-status `RecomputeJobStatus`) are not fully serializable on the
job side. Locking the job row would close the residual TOCTOU.

## Context
Surfaced by the 2026-06-26 `/roadmap deep` gaps sweep as a lower-confidence, belt-and-suspenders
companion to the retry/cancel epoch-fence bug. The epoch fence already covers the task side; this is
the job-side hardening.

## Repro / Symptoms
Two concurrent `DELETE /v1/jobs/{id}` cancels, or a cancel arriving as the agent posts a task's
terminal status, both read the job row unlocked at `internal/api/jobs.go:694-748` before mutating it.
No corruption has been observed (the task-side epoch bump covers the main hazard); this is a
serializability gap, not a confirmed data-corruption path.

## Proposal
Add a `FOR UPDATE` to the job read in `handleCancelJob` (a `GetJobForUpdate` query used inside the
cancel transaction) so the terminal-state check and the subsequent `CancelJobTasks`/`UpdateJobStatus`
writes are serialized against concurrent cancels and terminal recomputes.

## Acceptance / Done When
- `handleCancelJob` locks the job row before its terminal-state check, inside the same transaction as
  the cancel writes.
- Concurrent cancels serialize rather than interleave; behavior is otherwise unchanged.
- Coverage for the concurrent-cancel path.

## Related
- Same TOCTOU family as [[bug-2026-06-26-retry-resurrects-cancelled-task]]; can be scheduled with it.
- Source: `internal/api/jobs.go:694-748` (handleCancelJob), `internal/store/query/tasks.sql` (CancelJobTasks).

## Notes
Lower confidence than the retry bug - the epoch fence already prevents the concrete resurrect-a-task
corruption; this makes the job-side of cancel fully serializable.
