---
title: Jobs stats 24h buckets rely on updated_at finish proxy
type: bug
status: open
created: 2026-06-05
priority: low
source: jobs-list-frontend retro (2026-06-05)
---

# Jobs stats 24h buckets rely on updated_at finish proxy

## Summary
The `done_24h`/`failed_24h` buckets in `JobStatusCounts` window on `jobs.updated_at` as a finish-time proxy. This is correct today (the only writer of `updated_at` is `UpdateJobStatus`, and a terminal state is the last transition a job makes), but it would become inaccurate if a `POST /v1/jobs/:id/retry` endpoint is added that re-opens terminal jobs.

## Context
Decision recorded in the jobs-list design spec and verified during the session. Jobs have no dedicated `finished_at` column. If retry lands, revisit: either add a real `jobs.finished_at` column set on terminal transition, or window on `MAX(tasks.finished_at)`.

## Related
- `internal/store/query/jobs.sql` (`JobStatusCounts`)
- `docs/superpowers/specs/2026-06-05-web-jobs-list-design.md`
