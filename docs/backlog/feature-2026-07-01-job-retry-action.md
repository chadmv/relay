---
title: "Job retry action (retry failed / all tasks)"
type: feature
status: open
created: 2026-07-01
priority: medium
source: carved from feature-2026-06-26-job-actions-submit-cancel-retry during 2026-07-01 job-cancel-actions
---

# Job retry action (retry failed / all tasks)

## Summary
A Retry action in the job-detail header (`?task=failed|all`) that re-runs a terminal job's failed or
all tasks, carved out of the job write-actions item.

## Context
The 2026-07-01 job-cancel-actions slice deliberately narrowed the parent
[[feature-2026-06-26-job-actions-submit-cancel-retry]] to just cancel and force-cancel. Retry was
split out here because it is backend-blocked and has real correctness dependencies that cancel does
not.

## Blocked
**NO LONGER BLOCKED as of 2026-08-13.** Both prerequisites are resolved; this item is FE-ready.
- The `POST /v1/jobs/{id}/retry` route **now exists**, shipped in the 2026-08-13-job-retry-endpoint
  slice ([[feature-2026-08-13-job-retry-endpoint]], now in `docs/backlog/closed/`). The wire contract
  the FE must implement against, which differs from what this item assumed: `?task=failed|all` is
  **required** with no default - absent, empty, repeated or unrecognized values are a 400 - and
  `failed` reopens `failed` **and `timed_out`**. Auth is owner-or-admin with a **404** on deny. A
  success is 200 carrying the job plus `tasks_retried`, always `>= 1`. There is **no successful
  no-op**: nothing-matched, blocked-by-dependents and raced are all **409** with distinct messages,
  so the FE needs a 409 surface, not just a success/failure toggle.
- Retry re-opening terminal jobs did **not** reactivate
  [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]]. That slice measured it: a retried job leaves
  both 24h buckets the instant it becomes `running` and re-enters with a correct new `updated_at`
  when it finishes again, so the only effect is a transient undercount that self-corrects. Accepted
  in writing at the statement. That bug stays open on its own merits, but it is not a blocker here.

## Known trap for the FE slice
The retry 200 reports `total_tasks: 0` / `done_tasks: 0`, because the enrichment fields are only
populated by the list-row converter. Do **not** seed the job cache from this response or it will
overwrite real counts with zeros. Tracked as
[[bug-2026-08-13-single-job-responses-report-zero-total-tasks]].
- ~~Retry re-queues tasks and so must respect the epoch fence per
  `bug-2026-06-26-retry-resurrects-cancelled-task`.~~ **Resolved 2026-08-12** - that item is closed
  (`docs/backlog/closed/bug-2026-06-26-retry-resurrects-cancelled-task.md`). It is **not** a blocker
  any more, but do not read the fix as clearing the way to reuse the retry statement: it did the
  opposite. `IncrementTaskRetryCount` now fences on `assignment_epoch`, `worker_id` and
  `status IN ('pending','dispatched','running')`, which are the **exact inverse** of this feature's
  preconditions - a retry endpoint reopens tasks that ARE terminal and has no worker identity to
  bind, so every predicate would reject every call. The backend work must add its own statement
  (`status IN ('failed','timed_out')` allow-list, its own epoch bump); see
  [[feature-2026-08-13-job-retry-endpoint]], where that constraint is now recorded in full.

## Proposal
Once the backend route lands and the two bugs above are addressed, the frontend wiring mirrors the
cancel action:
- Follow the `useJobActions` hook pattern (single mutation, `?task=failed|all` as a call-site arg).
- Gate the action behind a `ConfirmDialog` with retry-specific copy.
- Use the same three-key invalidation on success (`['job', id]` + `['jobs']` + `['job-stats']`).
- Live in the job-detail header alongside the cancel action.

## Acceptance / Done When
- The job-detail header exposes a Retry action for terminal jobs, wired to
  `POST /v1/jobs/{id}/retry` with `?task=failed|all`.
- Retry is only offered once the backend route exists and the epoch-fence and jobs-stats bugs are
  resolved (or explicitly accepted as part of that work).
- FE tests mirror the cancel-action coverage (mutation wiring, invalidation, confirm gating).

## Related
- Carved from [[feature-2026-06-26-job-actions-submit-cancel-retry]] during the 2026-07-01
  job-cancel-actions slice.
- Backend route tracked in [[feature-2026-08-13-job-retry-endpoint]] (previously in
  [[feature-2026-06-26-web-enabler-backend-endpoints]], split 2026-08-13).
- Correctness deps: [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]] (still open);
  `bug-2026-06-26-retry-resurrects-cancelled-task` (closed 2026-08-12, now a constraint on the
  backend statement rather than a blocker - see Blocked above).
- Design: `design_handoff_relay_holo/reference/screens/job-detail.js`
- Source: `internal/api/jobs.go`, `web/src/jobs/`

## Notes
Was backend-blocked; no longer is, as of 2026-08-13. The FE wiring is a near-mirror of the cancel
action, with one shape difference worth planning for: cancel has no mode parameter and no rich
conflict vocabulary, whereas retry needs a `failed`-versus-`all` choice in the UI and must render
three distinguishable 409 reasons rather than a generic failure.
