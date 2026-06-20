---
title: One poisoned schedule aborts the whole schedrunner tick and hot-loops; reconcile falsifies last_run_at
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-20
priority: medium
source: full-codebase review (2026-06-10)
---

# One poisoned schedule aborts the whole schedrunner tick and hot-loops; reconcile falsifies last_run_at

## Summary
Two related schedrunner issues. (1) `TickOnce`'s comment claims a failed fire "still advances next_run_at via the same tx", but in Postgres an errored statement aborts the transaction: every subsequent statement fails and the commit fails. A persistent SQL error in one schedule's `createJob` means nothing in the batch commits (including advances for healthy schedules), and the failing schedule stays overdue, sorts first, and is retried every 10s forever, starving all other due schedules. (2) `AdvanceScheduledJob` unconditionally sets `last_run_at = NOW()`, and `ReconcileOnStartup` reuses it to skip missed runs, so every restart with overdue schedules records a run that never happened.

## Proposal
- Wrap each row in a savepoint (pgx nested transaction): `sp, _ := tx.Begin(ctx)`, rollback the savepoint on fire failure, then re-run advance on the outer tx so `next_run_at` still moves. Requires `fireOne` to return an error instead of swallowing it.
- Add a separate `AdvanceScheduledJobNextRun :exec` that sets only `next_run_at` and `updated_at` for the reconcile path, keeping `last_run_at` semantics as "last actual fire".

## Related
- `internal/schedrunner/runner.go:50-70` (`TickOnce`), `:221-224` (`ReconcileOnStartup`)
- `internal/store/query/scheduled_jobs.sql:61-67` (`AdvanceScheduledJob`)

## Resolution
Fixed in PR #40, 2026-06-20. Each schedule fire in `TickOnce`
is now wrapped in a pgx savepoint (`tx.Begin(ctx)` = `SAVEPOINT`, bound via
`r.q.WithTx(sp)`). On success `sp.Commit()` (RELEASE) keeps job creation and the
`last_run_at` advance atomic inside the savepoint. On failure `sp.Rollback()`
(ROLLBACK TO SAVEPOINT) clears the aborted-statement error and leaves the outer
tx usable, then `next_run_at` is advanced on the OUTER tx via a new
`AdvanceScheduledJobNextRun` query (next_run_at + updated_at only, NOT
last_run_at) so the poisoned schedule moves forward and stops hot-looping while
healthy schedules in the batch still commit. `fireOne` now returns an error
instead of swallowing it. `ReconcileOnStartup` also switched to
`AdvanceScheduledJobNextRun`, so a restart no longer falsifies `last_run_at` for
missed runs that never executed. Savepoint semantics were verified against the
pgx v5.9.1 source (no savepoint leak). Plan at
`docs/superpowers/plans/2026-06-20-schedrunner-poisoned-tick.md`.
