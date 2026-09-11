---
title: A PATCH landing between ReconcileOnStartup's read and its advance loses its recomputed next_run_at
type: idea
status: open
created: 2026-09-10
priority: low
source: Disclosed by the ReconcileOnStartup paging slice (PR #208), which narrowed the window and did not close it
---

# A PATCH landing between ReconcileOnStartup's read and its advance loses its recomputed next_run_at

## Summary

`AdvanceScheduledJobNextRun` carries no fence. A `handlePatchScheduledJob` that changes `cron_expr` or
`timezone` between the reconcile's page read and its UPDATE for that row has its freshly computed
`next_run_at` overwritten by one derived from the PRE-patch cron.

## Context

Disclosed in that statement's own SQL header rather than discovered later. The residual is bounded at
**one fire**: `fireOne` recomputes `next_run_at` from the row's current `cron_expr` at dispatch time,
so the row self-heals at its next firing regardless of how many patches preceded it. Two review lenses
attacked it and neither found a path where the clobber compounds.

**Paging NARROWED this window rather than widening it.** Before, one `SELECT` snapshotted the whole
table and the Go loop issued N sequential UPDATEs, so the last row's read-to-write gap spanned nearly
the whole pass. Now each page is read and advanced within the same iteration, so no row's gap exceeds
one page of processing, independent of total N - O(N) to O(page).

## Proposal

**This item must NOT prescribe a fence, and that is the whole design question.** A fenced non-match
SKIPS the advance, and a row left overdue is picked up by `ListEligibleScheduledJobs` within
`TickInterval` and fired once - which is exactly the spurious fire the never-catch-up policy exists to
prevent. So the obvious remedy trades a mistimed fire for an extra fire, and it is not obvious that is
better.

Options worth weighing: accept and document (the status quo), re-read the row inside the same
statement, or have the PATCH path itself recompute. Decide what an operator should observe before
picking.

## Acceptance / Done When

- A decision is recorded, with the reason a fence was or was not chosen.
- If the status quo is kept, the one-fire bound is stated where an operator reads schedule timing, not
  only in the SQL header.

## Related

- `internal/store/query/scheduled_jobs.sql` (`AdvanceScheduledJobNextRun`, `RecordScheduledJobFailure`),
  `internal/schedrunner/runner.go` (`ReconcileOnStartup`, `fireOne`),
  `internal/api/scheduled_jobs.go` (`handlePatchScheduledJob`)
- [[bug-2026-09-04-reconcileonstartup-lists-every-overdue-schedule-unbounded]] - closed; narrowed this
