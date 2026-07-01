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
**BLOCKED** on backend and correctness prerequisites:
- The `POST /v1/jobs/{id}/retry` route **does not exist yet** - tracked in
  [[feature-2026-06-26-web-enabler-backend-endpoints]].
- Retry re-opens terminal jobs, which reactivates the latent
  [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]] (the `done_24h`/`failed_24h` buckets window on
  `updated_at` as a finish proxy, which breaks once terminal jobs re-open).
- Retry re-queues tasks and so must respect the epoch fence per
  [[bug-2026-06-26-retry-resurrects-cancelled-task]] (the lone un-fenced `tasks.status` writer must
  not resurrect a cancelled/terminal task).

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
- Backend route tracked in [[feature-2026-06-26-web-enabler-backend-endpoints]].
- Correctness deps: [[bug-2026-06-05-jobs-stats-24h-updated-at-proxy]],
  [[bug-2026-06-26-retry-resurrects-cancelled-task]].
- Design: `design_handoff_relay_holo/reference/screens/job-detail.js`
- Source: `internal/api/jobs.go`, `web/src/jobs/`

## Notes
Unlike cancel and submit, retry is backend-blocked. Schedule it after the retry endpoint and the two
correctness bugs, then the FE wiring is a small mirror of the cancel action.
