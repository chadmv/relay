---
title: A permanently un-fireable schedule is invisible - a spec that stops validating advances next_run_at forever with only a log line
type: bug
status: open
created: 2026-08-23
priority: medium
source: 2026-08-23 deep roadmap refresh - backend invariants lens finding N4
---

# A permanently un-fireable schedule is invisible - a spec that stops validating advances next_run_at forever with only a log line

## Summary
`schedrunner.fireOne` re-validates the stored spec on every fire, because `CreateJobFromSpec`
calls `jobspec.Validate` (`internal/jobcreate/jobcreate.go:32`). On failure `TickOnce` logs one
line and calls `advanceNextRun` (`internal/schedrunner/runner.go:80-85`), so `next_run_at` keeps
marching. There is no `last_error` column in any migration and no failure field on
`scheduledJobResponse` (`internal/api/scheduled_jobs.go:19-34`): `last_run_at` and `last_job_id`
simply stop moving while `next_run_at` looks perfectly healthy, on the API, in the CLI, and on the
SPA's schedule pages.

## Context
This composes directly with [[bug-2026-08-12-retries-unvalidated-and-budget-only-in-go]]: adding a
`retries`/`timeout_seconds` bound to `jobspec.Validate` retroactively disables every stored
schedule that exceeds the new bound, with no operator-visible signal - which is why the roadmap
sequences this item beside that one. Also relevant: `handleCreateScheduledJob` stores the client's
raw bytes (`internal/api/scheduled_jobs.go:134`, `JobSpec: req.JobSpec`) - `ValidateJobSpec`
takes a value, so normalization is discarded and the stored spec is exactly what the client sent.

## Proposal
A `last_error TEXT` (nullable) + `last_error_at` on `scheduled_jobs`, written by the schedrunner
on a failed fire and cleared on a successful one, exposed on the response and rendered on the
schedule detail page (which already has the panel structure for it). Absent-not-zero applies: no
error means the fields are absent, not empty strings.

## Acceptance / Done When
- A schedule whose spec fails validation at fire time records the error where
  `GET /v1/scheduled-jobs/{id}` returns it, and a successful fire clears it.
- The schedule detail page surfaces the failure state without fabricating data the backend
  cannot supply.
- A regression test drives a stored-then-invalidated spec through a tick and asserts the error is
  visible via the API (RED at HEAD: nothing but a log line).

## Related
- `internal/schedrunner/runner.go:80-127`, `internal/jobcreate/jobcreate.go:32`, `internal/api/scheduled_jobs.go:19-34,134`
- [[bug-2026-08-12-retries-unvalidated-and-budget-only-in-go]] - land together; its bound is what makes this visible-failure surface urgent
- [[idea-2026-08-12-schedule-next-fires-preview]] - same response struct, different field
