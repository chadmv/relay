---
title: ReconcileOnStartup lists every overdue schedule with no LIMIT, also before the listener
type: bug
status: open
created: 2026-09-04
priority: medium
source: Spec and plan for the per-owner schedule cap (2026-09-04), which found it while checking what the paging slice actually bounded
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
