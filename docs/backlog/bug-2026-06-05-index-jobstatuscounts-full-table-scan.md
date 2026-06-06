---
title: Index JobStatusCounts to avoid full-table scan
type: bug
status: open
created: 2026-06-05
priority: low
source: jobs-list-frontend retro (2026-06-05)
---

# Index JobStatusCounts to avoid full-table scan

## Summary
`JobStatusCounts` (`internal/store/query/jobs.sql`) is a full-table `COUNT(*) FILTER` scan with no supporting index. A partial index on `jobs(status, updated_at)` would let the `GET /v1/jobs/stats` KPI endpoint use an index scan as the jobs table grows.

## Context
Surfaced in the code-quality review of the jobs-list-frontend work. The KPI strip polls `/v1/jobs/stats` every ~3s per active user, so the scan runs frequently. The `WorkerStatusCounts` aggregate shipped the same way, so the same index consideration applies to `/v1/workers/stats`.

## Related
- `internal/store/query/jobs.sql` (`JobStatusCounts`)
- `internal/store/query/workers.sql` (`WorkerStatusCounts`)
- `docs/retros/2026-06-05-web-jobs-list.md`
