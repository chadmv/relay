---
title: The watchdog sweep counters are never driven by real ListOverdueAssignedTasks rows, so the fake store's agreement with the real SQL is unchecked
type: idea
status: open
created: 2026-08-24
priority: low
source: Phase 4 integration lens of the 2026-08-24 silent-drop-observability-slice4 slice
---

# The watchdog sweep counters are never driven by real ListOverdueAssignedTasks rows

## Summary

Every assertion about `watchdog.counts.*` in the repo runs against a hand-written fake store. The
real-Postgres watchdog tests in `internal/worker/handler_watchdog_e2e_integration_test.go` sweep
genuine overdue rows in three tests and assert **nothing** about `CounterSnapshot()`.

No live defect is claimed. `ListOverdueAssignedTasks` requires `worker_id IS NOT NULL`, `worker_id`
is a native Postgres `UUID` column, and `uuidStr` renders any valid `pgtype.UUID` canonically - so
`canonicalWorkerKey`'s non-canonical branch really is unreachable today, as its comment says. The
value here is that the expensive fixture already exists and the missing assertion is a few lines.

## Context

The assumption under test is the one `canonicalWorkerKey` rests on: that every row the scan returns
has a `worker_id` renderable as a canonical uuid. That is currently established by reading the
migration, the query and `uuidStr` - three separate facts that a schema change could break
independently, with the failure surfacing as counts silently routed to `swept_overflow` rather than
as a red test.

Same family as [[idea-2026-08-20-claimtaskforworker-fixtures-bind-a-null-assigned-at]], which is
about claim fixtures omitting `AssignedAt` and thereby exempting themselves from the watchdog's
absolute arm - both are cases where a fixture agrees with itself rather than with the SQL.

## Proposal

Add counter assertions to the existing real-Postgres watchdog sweeps: after the sweep, assert
`swept_total` matches the number of rows swept, `swept_overflow` is zero, and every
`swept_by_worker` key is canonical. No new container, no new fixture - the sweeps are already there.

## Acceptance / Done When

- At least one integration-tagged test asserts the watchdog counters after a sweep of real rows.
- A schema or query change that produced a non-canonical or absent worker id would turn it RED
  rather than silently inflating `swept_overflow`.

## Related

- `internal/worker/handler_watchdog_e2e_integration_test.go` - the existing real sweeps.
- `internal/scheduler/watchdog_counters.go` - `canonicalWorkerKey` and its unreachable branch.
- [[idea-2026-08-20-claimtaskforworker-fixtures-bind-a-null-assigned-at]]
- [[idea-2026-08-24-counter-payload-guards-check-fixtures-not-producers]]
