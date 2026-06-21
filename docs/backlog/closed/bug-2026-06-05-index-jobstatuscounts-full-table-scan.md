---
title: Index JobStatusCounts to avoid full-table scan
type: bug
status: closed
created: 2026-06-05
closed: 2026-06-20
priority: low
source: jobs-list-frontend retro (2026-06-05)
---

## Resolution
Resolved 2026-06-20, folded into migration `000018_hot_path_indexes` (see
`feature-2026-06-10-hot-path-indexes`). Added `idx_jobs_status_updated ON jobs(status, updated_at)`
for `JobStatusCounts` and the symmetric `idx_workers_status_disabled ON workers(status, disabled_at)`
for `WorkerStatusCounts`. Correction to this item's original proposal: the index must be a
plain COVERING index, not partial - `JobStatusCounts` counts every row via `FILTER` with no
overall WHERE clause, so a partial index would exclude rows the aggregate must count. Both
KPI queries read only the two indexed columns, enabling an index-only scan in place of the
sequential heap scan.

# Index JobStatusCounts to avoid full-table scan

## Summary
`JobStatusCounts` (`internal/store/query/jobs.sql`) is a full-table `COUNT(*) FILTER` scan with no supporting index. A partial index on `jobs(status, updated_at)` would let the `GET /v1/jobs/stats` KPI endpoint use an index scan as the jobs table grows.

## Context
Surfaced in the code-quality review of the jobs-list-frontend work. The KPI strip polls `/v1/jobs/stats` every ~3s per active user, so the scan runs frequently. The `WorkerStatusCounts` aggregate shipped the same way, so the same index consideration applies to `/v1/workers/stats`.

## Related
- `internal/store/query/jobs.sql` (`JobStatusCounts`)
- `internal/store/query/workers.sql` (`WorkerStatusCounts`)
- `docs/retros/2026-06-05-web-jobs-list.md`
