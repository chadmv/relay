---
title: Per-worker activity aggregate (jobs or tasks today, failures, average duration) for the worker detail KPI
type: feature
status: open
created: 2026-09-01
priority: low
source: carved out of feature-2026-06-05-worker-detail-activity-panel at spec time (2026-09-01-worker-detail-tasks-panel-design.md, Decision 2)
---

# Per-worker activity aggregate for the worker detail KPI

## Summary
The worker detail page's "Jobs today" KPI is still a placeholder. The current-tasks panel and the
Slots KPI shipped on `GET /v1/workers/{id}/tasks`, but a windowed per-worker aggregate (count,
failures, average duration over the last 24h) was deferred because it needs an index, a migration,
a query and a product decision the list endpoint did not.

## Context
Three independent reasons it was carved out, any one sufficient. No index covers a per-worker
scan of terminal tasks: a Postgres foreign key creates none, and `idx_tasks_worker_active` is
partial over the currently-assigned partition, so a 24h aggregate on a 3s poll is a sequential scan of
`tasks`. "Jobs today" is a category error as labelled: relay assigns tasks to workers, `jobs` has
no worker column and one job spans workers, so whether the KPI counts distinct jobs or tasks is a
product decision. And the list slice was already one PR.

## Proposal
- Decide the unit (distinct jobs versus tasks) and rename the KPI label to match.
- Add a partial index on `tasks(worker_id, finished_at) WHERE status IN ('done','failed','timed_out')`
  with its migration; write the status predicate as an allow-list and add the statement to
  `TestTasksStatusVocabularyIsExactly`'s census.
- One windowed aggregate statement and either a field on the worker response or a small
  `GET /v1/workers/{id}/activity` endpoint under the same `auth(...)` posture as the worker reads.
- Wire the KPI; the `KpiStat label="Jobs today"` placeholder comment in `WorkerDetailPage.tsx`
  points at this item.

## Acceptance / Done When
- The KPI renders a real number from the server with the unit named in its label.
- The aggregate query uses the new index (EXPLAIN in the integration test or a stated measurement).
- The placeholder comment and the item pointer are gone.

## Related
- [[feature-2026-06-05-worker-detail-activity-panel]] (the parent, closed with this carved out)
- `docs/superpowers/specs/2026-09-01-worker-detail-tasks-panel-design.md` (Decision 2)
- [[feature-2026-09-01-per-worker-task-history]] would share the index
