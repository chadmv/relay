---
title: Job priority is validated, stored, sorted, and displayed, but the dispatcher never reads it
type: feature
status: open
created: 2026-08-23
priority: medium
source: 2026-08-23 deep roadmap refresh - gaps agent finding
---

# Job priority is validated, stored, sorted, and displayed, but the dispatcher never reads it

## Summary
`jobspec.Validate` enforces the `low`/`normal`/`high` vocabulary
(`internal/jobspec/jobspec.go:83-88`), the API sorts by priority, and the SPA renders a priority
chip - but `GetEligibleTasks` orders by `t.created_at` alone
(`internal/store/query/tasks.sql:187-197`) and `internal/scheduler/` contains zero references to
priority, so a `high` job queues behind every earlier `normal` task. README admits it
("priority-based scheduling is not implemented", ~line 1557), but no backlog item tracked it.

## Context
Under the project's own "omit data the backend cannot supply rather than fabricating it" rule, a
priority chip with no scheduling meaning is exactly the shape that warrants a filed enabler - the
UI currently implies a semantic the orchestrator does not have. Filed by the 2026-08-23 gaps pass.

## Proposal
Add priority to `GetEligibleTasks`' ordering (`ORDER BY priority DESC, created_at`), which also
intersects [[bug-2026-08-09-task-list-ordering-has-no-tiebreaker]] - both change ordering on the
same query family, so decide them together. Starvation semantics (does an endless stream of `high`
starve `normal`?) is the design question to answer and record; strict priority with FIFO within a
band is the simplest honest answer.

## Acceptance / Done When
- With a free worker and both a `high` and an earlier `normal` task eligible, the `high` task
  dispatches first (RED at HEAD).
- The chosen starvation semantics are stated in the query comment and README.
- README's "not implemented" sentence is updated.

## Related
- `internal/store/query/tasks.sql:187-197`, `internal/jobspec/jobspec.go:83-88`, `internal/scheduler/dispatch.go`
- [[bug-2026-08-09-task-list-ordering-has-no-tiebreaker]] - same ORDER BY family, decide together
- [[bug-2026-08-23-dispatch-cycle-unbounded-per-tick]] - any LIMIT added there must respect priority ordering or it silently reintroduces starvation
