---
title: handleCancelJob reads the job row without FOR UPDATE (cancel not fully serializable)
type: bug
status: closed
created: 2026-07-01
closed: 2026-08-13
resolution: fixed
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
- Same TOCTOU family as `bug-2026-06-26-retry-resurrects-cancelled-task`, which was closed
  2026-08-12 (`docs/backlog/closed/`) - so this can no longer be scheduled alongside it. Note that
  close narrows this item: the retry side of the race is now fenced on epoch, assignee and
  terminality, so a cancel landing in the retry window wins at the statement. What remains here is
  the read-then-write window inside `handleCancelJob` itself.
- Source: `internal/api/jobs.go:694-748` (handleCancelJob), `internal/store/query/tasks.sql` (CancelJobTasks).

## Notes
Lower confidence than the retry bug - the epoch fence already prevents the concrete resurrect-a-task
corruption; this makes the job-side of cancel fully serializable.

## Resolution

Closed as a side effect of the 2026-08-13-job-retry-endpoint slice, which nobody had scheduled
against this item - it was found during that slice's retro, not planned.

`handleCancelJob` now reads the job through the new `GetJobForUpdate` statement inside its own
transaction, and the terminal-state check reads that locked row, so the check and the subsequent
`CancelJobTasks` / `UpdateJobStatus` writes are serialized against a concurrent cancel or terminal
recompute. That is acceptance bullets 1 and 2 exactly as written.

The retry endpoint is why it happened: `handleRetryJob` is the second multi-statement writer over
jobs+tasks, and with cancel reading tasks-then-job the two were an ABBA deadlock pair reachable by
two ordinary operator actions. Both handlers now take one lock order - job, then tasks - and the
`GetJobForUpdate` comment records that ordering as load-bearing so neither is "optimized" back.

One deliberate caveat, recorded rather than glossed. Acceptance bullet 3 asked for coverage of the
concurrent-cancel path; what shipped covers cancel-versus-**retry**
(`TestRetryJob_CancelSerialization_NeverCancelledJobWithPendingTasks`, 5/5 stable at `-count=5`,
10 racing rounds per invocation), not cancel-versus-cancel. The lock that serializes the one
serializes the other - it is the same row lock taken at the same point - so the mechanism is
covered even though that specific pairing has no dedicated test. Flagged here so a future reader
does not assume a cancel-versus-cancel test exists.

A second gate change rode along: the owner-or-admin check now runs on an **unlocked** read taken
before the transaction opens, so an unauthorized caller cannot queue on a victim's job row lock
before receiving its 404. That was a regression this slice introduced and then fixed, not
something this item asked for.
