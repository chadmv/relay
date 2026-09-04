---
title: The active-task index predicate guard only reads quoted literals, so a conjunct is invisible to it
type: idea
status: open
created: 2026-09-04
priority: low
source: Phase 4 invariants lens on the preparing-task-status slice; measured against a real Postgres by the integration lens
---

# The active-task index predicate guard only reads quoted literals, so a conjunct is invisible to it

## Summary

`TestActiveTaskIndexPredicateNamesTheExpectedStatuses` reads `idx_tasks_worker_active`'s predicate
back off `pg_get_expr` and extracts it with a `'([^']*)'` regex. That sees quoted literals and
nothing else, so an added CONJUNCT - `... AND worker_id IS NOT NULL` - contributes no literal and
leaves the guard green while changing what the index implies.

## Context

Measured rather than predicted. The index was widened with that conjunct against a live Postgres and
`EXPLAIN` run for all four assignment-partition consumers: `GetActiveTasksForWorker`,
`CountActiveTasksByAllWorkers`, `ListOverdueAssignedTasks` and `ListGraceCandidates`. All four still
used the widened index, because Postgres derives `NOT NULL` from a strict `=` or an explicit
`IS NOT NULL` and every current consumer carries one.

So this is a **blindness, not a live regression**. It would bite a future query on this partition
that carries no `worker_id` predicate of its own - at which point the index silently stops being
usable for it and the guard says nothing.

The hard-coded expectation is deliberate and must stay: a guard that derived its expectation from
the index it guards could not fail. The gap is the extraction, not the expectation.

## Proposal

Assert the predicate's SHAPE, not only the literals it contains - for example, normalise the
`pg_get_expr` output and compare it against an expected expression, so any added term fails rather
than being skipped. Alternatively assert the implication directly by `EXPLAIN`-ing one representative
consumer, accepting that a plan-based test is more brittle.

## Acceptance / Done When

- Adding a conjunct to `idx_tasks_worker_active`'s predicate turns the guard red.
- The expectation still does not derive from the index under test.

## Related

- `internal/store/active_task_index_predicate_integration_test.go`
- `internal/store/migrations/000023_task_preparing_status.up.sql`
- [[feature-2026-09-01-worker-activity-aggregate]] - a future consumer of this partition
