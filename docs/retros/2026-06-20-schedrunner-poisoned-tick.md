---
date: 2026-06-20
topic: schedrunner-poisoned-tick
branch: claude/strange-nobel-08bd7a
pr: "#40"
merge: "see PR #40"
---

# Session Retro: 2026-06-20 - Schedrunner Poisoned Tick + Reconcile last_run_at

**TL;DR:** Closed `bug-2026-06-10-schedrunner-poisoned-tick` by wrapping each
schedule fire in `TickOnce` in a pgx savepoint, so one poisoned schedule's
failing `CreateJobFromSpec` no longer aborts the whole shared tick transaction
(which had been losing healthy schedules' advances and leaving the bad schedule
to hot-loop every 10s). On savepoint rollback the poisoned schedule still
advances `next_run_at` on the outer tx via a new `AdvanceScheduledJobNextRun`
query, so it stops hot-looping while healthy schedules commit. The same query
fixes `ReconcileOnStartup`, which previously reused `AdvanceScheduledJob` and
falsely recorded `last_run_at` for missed runs that never executed.

## What Was Built

A single bad schedule can no longer starve every other due schedule or record
phantom runs. Two distinct defects in one fix:

- **Savepoint-per-fire in `TickOnce`.** Each row's fire opens a savepoint
  (`tx.Begin(ctx)` = `SAVEPOINT`) bound via `r.q.WithTx(sp)`. On success
  `sp.Commit()` (RELEASE) keeps create+advance atomic, with the success path
  setting `last_run_at` inside the savepoint. On failure `sp.Rollback()`
  (ROLLBACK TO SAVEPOINT) clears the aborted-statement error and leaves the
  OUTER tx usable, then `next_run_at` is advanced on the outer tx via
  `AdvanceScheduledJobNextRun`. The poisoned schedule still moves forward (no
  hot-loop) and the healthy schedules in the batch still commit. `fireOne` now
  returns an error instead of swallowing it, and returns the intended failure
  next-fire time so the outer-tx advance uses the right timestamp.
- **`AdvanceScheduledJobNextRun` (next_run_at + updated_at only, NOT
  last_run_at).** Used by both the failure path and `ReconcileOnStartup`, so a
  restart that advances past overdue schedules no longer falsifies
  `last_run_at`. `last_run_at` now means "last actual fire".

Plan at `docs/superpowers/plans/2026-06-20-schedrunner-poisoned-tick.md`.

## Key Decisions

- **Savepoint wrapping lives in `TickOnce`, not `fireOne`.** `fireOne` operates
  purely on the savepoint-bound queries; the advance-on-failure happens on the
  outer tx. This keeps the success advance atomic with job creation (inside the
  savepoint) while keeping the failure advance on a clean outer tx after the
  rollback - which is precisely what prevents both the batch abort and the
  hot-loop at once.
- **Failure path advances next_run_at only.** No job ran, so setting
  `last_run_at` would be the same falsification the reconcile bug exhibited.

## Problems Encountered

- **Savepoint semantics were the load-bearing detail, so they were verified
  against the library source.** Phase 4 verify confirmed against the pgx v5.9.1
  source that `tx.Begin` on an open tx issues `SAVEPOINT` (a pseudo-nested tx,
  not a new DB transaction) and that `sp.Rollback` issues `ROLLBACK TO
  SAVEPOINT`, which clears the aborted-statement error and leaves the outer tx
  committable. Verify also checked there was no savepoint leak. When a fix
  depends on exact transaction-control semantics of a dependency, read that
  dependency's source rather than trusting the mental model.
- **Simulating the failure required violating a DB constraint, restored in
  `t.Cleanup`.** `scheduled_jobs.owner_id` and `job_spec` both reject bogus
  data (FK + JSONB validation), so the integration test had no clean way to
  plant a row whose `CreateJobFromSpec` fails inside the savepoint. The lever:
  temporarily drop and re-add the `owner_id` FK to plant a poison row whose
  `CreateJob` fails on the `jobs.submitted_by` FK instead. The constraint
  restore was hardened into `t.Cleanup` so a mid-test failure cannot leave the
  (per-test, throwaway) container's schema inconsistent. Lesson: when a test
  must violate a constraint to simulate a failure, restore it in `t.Cleanup`,
  not inline.

## Improvement Goals

- **Read the dependency source when a fix hinges on its transaction-control or
  concurrency semantics.** The savepoint behavior could have been assumed; it
  was instead confirmed against pgx v5.9.1, which is what gave confidence the
  outer tx survives a rolled-back savepoint. Adopt this whenever correctness
  rests on a library's exact behavior rather than its documented contract.
- **Constraint-violating test setup must self-restore via `t.Cleanup`.** A test
  that drops a constraint to manufacture a failure owns restoring it
  unconditionally, not on the happy path, so a mid-test failure cannot corrupt
  shared (or reused) schema.

## Files Most Touched

- `internal/schedrunner/runner.go` - savepoint-per-fire in `TickOnce`; `fireOne`
  returns an error and the failure next-fire time; `ReconcileOnStartup` switched
  to `AdvanceScheduledJobNextRun`.
- `internal/store/query/scheduled_jobs.sql` - new `AdvanceScheduledJobNextRun`
  (next_run_at + updated_at only).
- `internal/schedrunner/runner_test.go` - integration test planting a poison row
  via a temporarily-dropped FK, restored in `t.Cleanup`.
- `docs/superpowers/plans/2026-06-20-schedrunner-poisoned-tick.md` - plan.
- `docs/backlog/bug-2026-06-10-schedrunner-poisoned-tick.md` - closed.
