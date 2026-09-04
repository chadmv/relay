---
title: A rolling multi-replica deploy tells an agent to cancel its in-flight workspace sync
type: bug
status: open
created: 2026-09-04
priority: medium
source: Phase 4 correctness and security lenses on the preparing-task-status slice; reproduced against a real Postgres by the integration lens
---

# A rolling multi-replica deploy tells an agent to cancel its in-flight workspace sync

## Summary

Migrations run at server startup, so the first new replica widens the schema while old replicas are
still serving. A `preparing` row written by a new replica is invisible to every old replica's copy
of the assignment partition. The loudest consequence is not silent log loss: `reconcileRunningTasks`
misses the task in `serverSet` and tells the agent to CANCEL a healthy multi-hour sync.

## Repro / Symptoms

Reproduced against a real Postgres by replaying `reconcileRunningTasks`' set-comparison against two
data sources for the same seeded row: the pre-000023 predicate and the current statement.

    OLD replica: active=map[]                 cancelIDs=[<task-id>]
    NEW replica: active=map[<task-id>:1]      cancelIDs=[]

Three further effects on an old replica, all from the same cause: `RequeueTaskByID`,
`RequeueWorkerTasks` and `ListGraceCandidates` exclude the row, so a disconnect does not release it;
`CountActiveTasksByAllWorkers` does not count it, so that replica's dispatcher over-issues the
worker's slots; and `ListOverdueAssignedTasks` does not scan it, so no old replica sweeps it. The
state self-heals only once the deploy completes, bounded meanwhile by whichever new replica runs the
sweep at `RELAY_TASK_MAX_ASSIGNMENT` (24h default).

An agent chooses when to send `PREPARING` and can force a reconnect, so it can extend the exposure -
but it cannot open the window, which is operator-controlled and short.

## Proposal

Decide and write down which of these the project wants, where the next status-widening slice will
find it - `prepare_failed` and a task-level `cancelled` will both hit the same window:

- an operator runbook step (drain agents, or accept the window) documented in README beside the
  multi-replica section; or
- a release gate: the new status is written only when an env flag is set, so one release teaches
  every replica to READ it and the next release starts WRITING it.

The generalisable half is the more valuable one: any widening of a partition that several replicas
read has this shape, and nothing in the tree currently says so.

## Acceptance / Done When

- The mixed-version window is documented or gated, and the choice is recorded with its reasoning.
- Whatever is chosen is stated somewhere a future status-widening slice will read it, not only in
  this item.

## Related

- `internal/worker/handler.go` `reconcileRunningTasks`, `internal/store/query/tasks.sql`
- `docs/superpowers/specs/2026-09-03-preparing-task-status.md` - the slice that opened it; it
  enumerates consumers within one binary and says nothing about consumers at another version
- [[feature-2026-09-03-preparing-task-status]]
