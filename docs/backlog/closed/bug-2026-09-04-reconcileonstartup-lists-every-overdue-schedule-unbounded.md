---
title: ReconcileOnStartup lists every overdue schedule with no LIMIT, also before the listener
type: bug
status: closed
created: 2026-09-04
priority: medium
source: Spec and plan for the per-owner schedule cap (2026-09-04), which found it while checking what the paging slice actually bounded
closed: 2026-09-10
resolution: fixed
---

# ReconcileOnStartup lists every overdue schedule with no LIMIT, also before the listener

## Summary

`ListOverdueScheduledJobsForCatchup` is `SELECT * FROM scheduled_jobs WHERE enabled AND next_run_at < NOW();` with **no LIMIT**. `ReconcileOnStartup` is its only caller, and `cmd/relay-server`'s `main` calls it above the goroutine that starts `srv.ListenAndServe()`. So the boot materializes every overdue enabled row before the server accepts a request.

## Context

Found while writing `docs/superpowers/specs/2026-09-04-per-owner-schedule-cap.md` and confirmed independently by that spec's plan.

**This is the same shape as the boot sweep, in the same function's neighbourhood, and the paging slice did not touch it.** `docs/superpowers/specs/2026-09-04-boot-sweep-keyset-paging.md` keyset-paged `ValidateStoredSpecsOnStartup` and bounded that sweep's peak memory to one page. The natural reading of the result - "the boot's peak memory is one page" - is true of the sweep and **false of the boot**, because this second unbounded read runs on the same path.

It is not a duplicate of the sweep item: different statement, different caller, and paging one says nothing about the other. It is also not closed by
[[feature-2026-09-04-per-owner-schedule-cap]] - a per-owner cap bounds how many rows one owner contributes, not the total, and this query's result set is every overdue enabled row across all owners.

The severity is bounded by how many schedules are overdue at boot rather than by table size, so a healthy deployment reads few rows. The bad case is a deployment that has been down long enough for a large fraction of its schedules to come due, which is exactly the restart where the boot is already slow.

## Proposal

Sketch only.

Page it the way `ValidateStoredSpecsOnStartup` is now paged, or bound it and record what happens to the remainder. Note the reconcile has a semantic the sweep does not: it advances `next_run_at` past missed runs (never-catch-up), so a bounded read that silently skips rows leaves those schedules still overdue on the next boot. Decide whether the remainder is processed in a later pass, on the ticker, or not at all - and say so where an operator reads it.

Check whether the same reasoning applies to any other pre-listener query.

## Acceptance / Done When

- The boot's peak memory does not grow with the number of overdue schedules.
- What happens to rows beyond any bound is decided and documented, not left implicit.
- The claim that the boot's peak memory is one page is either made true or corrected wherever it is written down.

## Related

- `internal/schedrunner/startup_validation.go` (`ReconcileOnStartup`),
  `internal/store/query/scheduled_jobs.sql` (`ListOverdueScheduledJobsForCatchup`),
  `cmd/relay-server/main.go` (the call site, above `ListenAndServe`)
- [[bug-2026-08-28-boot-sweep-lists-every-schedule-ahead-of-the-listener]] - the sibling this was found beside
- [[feature-2026-09-04-per-owner-schedule-cap]] - does not close this

## Resolution

Fixed in PR #208, squashed as `3dc21367`. `ListOverdueScheduledJobsForCatchupPage` is keyset-paged on
`id` with an explicit `cursor_set` bool, `ORDER BY id`, an exact `LIMIT` with no `+1`, terminating on a
short page, and narrowed from `SELECT *` to the four columns the loop reads. Paged to exhaustion, no
remainder and no ceiling. `reconcilePageSize = 100`, its own constant - deliberately not aliased to
`BatchLimit` or `sweepPageSize`, which govern different policies.

**This item points implementers at the wrong file.** Its Related section says
`internal/schedrunner/startup_validation.go (ReconcileOnStartup)`. The function is in `runner.go`;
`startup_validation.go` names it only in prose - and that file holds an already-paged function whose
header reasons at length about paging, so the likely outcome of the miscitation is a patch to the wrong
function.

**"Before the server accepts a request" is true of HTTP and false of gRPC.** `grpcSrv.Serve`'s goroutine
and `dispatcher.Run` both start above the reconcile call; only `ListenAndServe` is below it. Agents
connect, register and receive dispatched work throughout the pass, while a readiness probe on `:8080`
fails.

**The third acceptance criterion was substantially already green.** A complement search found no site
asserting the BOOT's peak memory is one page - every hit was sweep-scoped and true. A criterion green
before the change pins nothing, so the slice did not manufacture work for it.

**The rejected design is the load-bearing half.** Re-issuing the predicate until a short page, relying
on each row's own advance, does NOT terminate, on three counts all live in the body at the time: a
`ParseSchedule` failure continues without advancing, the advance's error is logged rather than returned,
and a fresh per-page `NOW()` lets a short-interval schedule re-enter. `id > cursor_id` makes
termination depend on the cursor alone rather than on the loop body succeeding at anything.

**And the test that proves it had to be designed for it.** A guard planting "at least one"
unparseable-cron row does not redden that design: healthy rows advance out of the predicate, the loop
ends on a short page, and the mutant passes. The load-bearing property is P >= page size, so the fixture
plants 150 poisoned against a page of 100. Two mutations survive the page-count guard entirely - a
min-cursor and the no-cursor drain loop - because the reconcile writes the column its own predicate
selects on; both are recorded as survivals rather than kills the test did not earn. The no-cursor design
dies as a bounded 60.55s timeout naming the stuck cursor.

Review found two false claims in the slice's own prose. The SQL comment argued no fence was needed
because the statement "writes a VALUE computed from cron_expr and timezone, so two replicas write the
same value" - false, since the value is `sched.Next(now)` with each replica's own clock, and it
contradicted the function's own header three paragraphs up. And the `isSweepPageRead` matcher fix was
one-sided; making it reciprocal revealed that excluding on the bare column `next_run_at` matches
NOTHING, because sqlc expands the sweep's `SELECT *` into a column list that itself contains that
column. The needle has to be the whole predicate.

README's startup sequence is also corrected: it had listed the HTTP server BEFORE the reconcile - the
inverse of the property this item is about - and omitted the validation sweep entirely.

**Duration remains bounded by nothing**, and the function header says so. That is
[[feature-2026-09-04-wall-clock-deadline-on-the-boot-sweep]]. One further pre-listener statement nobody
had named was found and left in scope for a separate item: `ListGraceCandidates`, a `SELECT DISTINCT`
with no LIMIT.
