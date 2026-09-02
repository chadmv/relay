---
title: Per-worker task history (terminal tasks for a worker)
type: feature
status: open
created: 2026-09-01
priority: low
source: carved out of feature-2026-06-05-worker-detail-activity-panel at spec time (2026-09-01-worker-detail-tasks-panel-design.md, Decision 1)
---

# Per-worker task history

## Summary
`GET /v1/workers/{id}/tasks` returns only the tasks currently assigned to a worker (dispatched
and running). There is no per-worker route for terminal tasks, so an operator asking "what did
this node run today, and what failed" has to reconstruct it from `GET /v1/jobs` plus
`GET /v1/jobs/{id}` and the `worker_id` on each task.

## Context
Widening the active-tasks endpoint with a `?status=` parameter was refused at spec time because
any widening is an unindexed sequential scan of `tasks`: the only per-worker index is partial
over `('dispatched','running')`. A history needs its own index and real paging.

## Proposal
- Extend `GET /v1/workers/{id}/tasks` with an allow-listed `?status=` parameter rather than a
  second route, returning terminal tasks ordered by `finished_at DESC, id DESC`.
- Add a partial index on `tasks(worker_id, finished_at) WHERE status IN ('done','failed','timed_out')`
  with its migration; add the statement to `TestTasksStatusVocabularyIsExactly`'s census.
- A history tab or panel on the worker detail page with a real pager.

## Related
- `docs/superpowers/specs/2026-09-01-worker-detail-tasks-panel-design.md` (Decision 1)
- [[feature-2026-09-01-worker-activity-aggregate]] shares the index
