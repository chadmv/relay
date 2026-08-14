---
title: "Job retry action (retry failed / all tasks)"
type: feature
status: closed
created: 2026-07-01
closed: 2026-08-14
resolution: fixed
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

## Resolution

Shipped in the 2026-08-14-job-retry-action slice, closing a loop this batch opened: the endpoint it
consumes, `POST /v1/jobs/{id}/retry`, shipped four iterations earlier in the same batch, and the
Phase 4 browser lane drove the one against the other for real rather than against a mock.

Two pills, `Retry failed` and `Retry all`, each behind its own confirm dialog. Two pills rather than
one control with a mode picker, because the endpoint has **no server-side default** for `?task` -
deliberately - so the UI must make the choice explicit. The precedent is the hi-fi's single `Abort`
shipping as `Cancel` plus `Force cancel`.

**The three 409s render as three different things**, which is the point of the slice. Nothing-matched
is a dead end, blocked-by-dependents is permanent for that job in that mode, and raced means try
again; a generic failure banner would hand the operator no next step. `retryError.ts` classifies on
the server's own sentence and adds a frontend-owned hint, falling through to the server's text
rather than a generic string when it cannot classify.

Verified end to end in a real browser against the shipped handler: `?task=failed` returned 200 with
`tasks_retried: 1`, a real `job is not finished` 409 rendered its specific sentence plus its hint and
was hit-tested as **not occluded** by any scrim, and the availability matrix held on all five job
statuses. Four pills at a 375px viewport produced no overflow, confirming the previous slice's
app-wide fix holds under a new worst case.

Decisions worth keeping:

- **Invalidate, never seed.** The retry 200 goes through `toJobResponse(job, "", nil, nil)`, so
  besides the zeroed counters this item already warned about it drops `tasks` and
  `submitted_by_email` entirely - seeding would have blanked the task table, not merely two numbers.
  `retryJob` returns only `{tasks_retried}`, so seeding will not compile.
- **Availability is an allow-list** (`done || failed`), never the equivalent deny-list, so a status
  added later fails closed.
- **The dialog closes before `mutate`**, so the classified banner is never painted behind its own
  scrim - the failure mode this repo recorded once already.
- The hi-fi is **not** silent here, contrary to what this item implied: it carries a `Retry` ghost
  pill, on a *running* job, which the endpoint refuses. This slice diverges deliberately on both
  count and availability.

Review found one medium, and it is the vacuity pattern this batch kept surfacing: the contract test
pinning the server's error prefixes did not actually pin the `raced` one, because `the job changed`
is also a substring of the *blocked* sentence. Rewording the handler's raced sentence left the test
green while the frontend would have silently degraded to `unknown`. The prefix is now the
branch-unique `the job changed while the retry was in flight`, and the test's real limit - it cannot
catch a wholly new 409 branch with no prefix entry - is written down rather than left to be
over-trusted.

Also fixed: the unknown fallback rendered `err.message`, which carries a numeric status prefix, so
an unclassified error showed the user `500 db error` while every sibling branch showed a bare
sentence. An existing assertion was pinning that defect and was corrected with it - the second time
in this batch a test was green *because of* the bug it covered.
