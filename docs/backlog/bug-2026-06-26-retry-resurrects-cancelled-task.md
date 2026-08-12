---
title: IncrementTaskRetryCount can resurrect a cancelled task (no epoch/status guard)
type: bug
status: open
created: 2026-06-26
priority: high
source: ROADMAP deep-refresh gaps sweep (2026-06-26)
---

# IncrementTaskRetryCount can resurrect a cancelled task (no epoch/status guard)

## Summary
`IncrementTaskRetryCount` is the only `tasks.status` writer with a bare `WHERE id = $1` - no
`assignment_epoch` fence and no `status IN (...)` guard. A cancel that lands in the retry TOCTOU
window can be clobbered: the retry flips a just-cancelled task back to `pending` and
`RecomputeJobStatus` then pulls the job out of `cancelled`, so the dispatcher re-runs work on a
cancelled job. This sidesteps the project's epoch-fence invariant, which every other status writer
honors.

## Context
Surfaced by the 2026-06-26 `/roadmap deep` gaps sweep as the one untested epoch-fence interleaving.
Today the retry path is only agent-internal (on task failure), so the window is narrow; it becomes
much more reachable once an operator-initiated retry endpoint lands (see Related).

## Repro / Symptoms
1. A task is running; the handler reads its `assignment_epoch` (`internal/worker/handler.go:419`).
2. An operator cancels the job: `CancelJobTasks` (`internal/store/query/tasks.sql:170-181`,
   intentionally un-fenced) sets the task `failed` and bumps the epoch; the job goes `cancelled`.
3. The handler's failure path calls `IncrementTaskRetryCount` (`:447-455`) which, with no epoch or
   status guard (`internal/store/query/tasks.sql:21-26`), unconditionally flips the task back to
   `pending` and re-bumps the epoch.
4. `RecomputeJobStatus` observes a non-terminal task and moves the job out of `cancelled`; the
   dispatcher re-runs a task on a job the operator cancelled.

Observed: cancelled job resurrected. Expected: a cancelled/terminal task is never re-queued.

## Proposal
Add a guard to `IncrementTaskRetryCount` so it only re-queues a task it still owns and that is not
terminal - either fence on `assignment_epoch` (match the caller's epoch, like `ClaimTaskForWorker`)
or add `AND status NOT IN ('failed','timed_out','done')` plus a job-not-cancelled check, or both.
Re-read state inside the write rather than trusting the epoch read at handler.go:419. Whichever
guard is chosen must end or match the assignment epoch per the invariant.

## Acceptance / Done When
- `IncrementTaskRetryCount` cannot move a cancelled/terminal task back to `pending`.
- A regression test covers the cancel-during-retry interleaving (the existing
  `internal/store/store_test.go:550` only covers the reverse, stale-update-after-retry direction);
  it is RED before the fix and GREEN after.
- The fix respects the epoch-fence invariant (CLAUDE.md) - no zero-epoch call, no return to
  `pending` without an epoch bump.

## Related
- Becomes operator-reachable via [[feature-2026-06-26-job-actions-submit-cancel-retry]] and
  [[feature-2026-06-26-web-enabler-backend-endpoints]] (the `POST /v1/jobs/{id}/retry` endpoint) -
  schedule this fix with that work.
- Same TOCTOU family as the optional `FOR UPDATE` hardening on `handleCancelJob`
  (`internal/api/jobs.go:694-748`), noted in the roadmap's Suggested backlog actions.
- Source: `internal/store/query/tasks.sql:21-26` (IncrementTaskRetryCount), `:170-181`
  (CancelJobTasks), `internal/worker/handler.go:419,447-455`, `internal/store/store_test.go:550`.

## Notes
This is the lone epoch-fence writer with no guard; the hardening phase closed the rest. Worth fixing
before the retry endpoint ships so the new entry point is safe by construction.

**Narrowed, not closed (2026-08-12).** The task-status assignee fence
(`docs/superpowers/specs/2026-08-12-taskstatus-update-assignee-fence.md`, section 3.4) added an
identity gate to `handleTaskStatus` that runs *ahead* of the retry branch, so
`IncrementTaskRetryCount`'s only production caller is now reachable only by the task's own assignee
at the current epoch. That closes the forged route into this query - an unrelated agent could
previously burn a retry on any task by guessing its epoch, NULLing `worker_id` and bumping the
epoch out from under the agent legitimately running it.

Two routes remain, and the second is worse than the cancel race this item was filed for:

1. The cancel-during-retry interleaving described above, plus whatever
   `POST /v1/jobs/{id}/retry` opens when it lands.
2. **A single-actor, race-free resurrection by the task's own assignee.** A terminal transition
   deliberately does not bump `assignment_epoch`, and now structurally keeps `worker_id` (the fence
   argument is no longer written), so the assignment survives completion by design - that is what
   lets a trailing `AppendTaskLog` chunk still pass its fence. The consequence is that the assignee
   can send `DONE` at epoch N, letting dependents dispatch, and then send `FAILED` at the same
   epoch N. Both gates still pass - it really is the assignee, and the epoch really is current -
   `terminal && task.RetryCount < task.Retries` holds, and `IncrementTaskRetryCount` moves the
   **already completed** task back to `pending` and re-dispatches it while its dependents are
   already running. No concurrency and no second actor required; a buggy or crash-looping agent
   reaches it by accident.

The structural fix for route 2 is a status predicate - `AND status NOT IN ('done','failed',
'timed_out')` on both `UpdateTaskStatus` and `IncrementTaskRetryCount` - and explicitly **not** an
epoch bump on terminal transitions, which would break the trailing-log flush that
`TestUpdateTaskStatus_TerminalTransitionDoesNotEndTheAssignmentSoTrailingLogsStillPersist` pins.

The query itself still has a bare `WHERE id = $1` and no status guard, so this item stays open and
its acceptance criteria are unchanged, except that they should now also cover route 2.
