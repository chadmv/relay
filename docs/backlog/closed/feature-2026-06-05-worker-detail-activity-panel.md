---
title: Worker detail running-tasks and activity stats panel
type: feature
status: closed
closed: 2026-09-02
resolution: fixed
created: 2026-06-05
priority: medium
source: deferred from the worker-detail-page read-only slice (2026-06-05 brainstorm)
---

# Worker detail running-tasks and activity stats panel

## Summary
The wireframe's "current tasks" panel (tasks currently dispatched/running on a
worker, with progress) and "jobs today" stat (count, failures, avg duration) were
dropped from the read-only worker-detail slice because no HTTP endpoint exposes
per-worker task data. Both need a new backend endpoint before the UI can be built.

## Context
Today only an internal aggregate exists (`ActiveTaskCounts` in
`internal/store/query/tasks.sql`, `worker_id, count(*)`), used by the scheduler -
not exposed over HTTP and not a per-task list. The wireframe `v3Detail` shows both
a live current-tasks table and a jobs-today summary.

## Proposal
- Add a backend endpoint to list tasks for a worker (e.g. `GET /v1/workers/{id}/tasks`),
  filtered to active statuses for the current-tasks table, with enough fields for
  task/job identity and progress.
- Optionally add a per-worker activity aggregate (jobs today, failures, avg duration)
  or derive it from the same data.
- Build the detail-page panels (current tasks table + activity stat card) on top.

## Acceptance / Done When
- An authenticated endpoint returns a worker's current/active tasks.
- The detail page shows current tasks and a today's-activity summary.
- Backend and frontend tests cover the new endpoint and panels.

## Related
- `internal/store/query/tasks.sql` (`ActiveTaskCounts`)
- `internal/api/tasks.go`, `internal/api/workers.go`
- `docs/superpowers/specs/2026-06-05-worker-detail-page-design.md`

## Resolution
GET /v1/workers/{id}/tasks lists a worker's currently assigned tasks (dispatched and running, an allow-list on the existing partial index, any authenticated user, page envelope with total as the dispatcher's used-slot count, assignment_epoch deliberately absent and pinned by closed-set key tests). The worker detail page renders a current-tasks panel on the holo primitives and the Slots KPI now shows the real used count. The item's second half, the jobs-today activity aggregate, is carved out to feature-2026-09-01-worker-activity-aggregate: it needs an index, a migration and a product decision (relay assigns tasks, not jobs, to workers). The item named ActiveTaskCounts, which does not exist; the statement is CountActiveTasksByAllWorkers and it has no worker filter.
