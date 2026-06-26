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
