---
title: No CHECK constraints on status vocabularies; JobStatusCounts counts statuses that are never written
type: bug
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

# No CHECK constraints on status vocabularies; JobStatusCounts counts statuses that are never written

## Summary
`workers.status`, `jobs.status`, `jobs.priority`, `tasks.status`, `task_logs.stream`, and `scheduled_jobs.overlap_policy` are free TEXT with no CHECK constraints, and drift is already visible: `JobStatusCounts` counts job statuses `dispatched`, `queued`, and `timed_out` that no code path ever writes to `jobs.status` (writers produce only `pending`/`running`/`done`/`failed`/`cancelled`), so those KPI buckets are permanently zero, while `cancelled` jobs are counted nowhere. A CHECK constraint would have caught this at integration-test time. Related: `jobspec` does not validate `Priority` either, so typos like "hgih" are stored silently.

## Proposal
- Add CHECK constraints in a migration, e.g. `ALTER TABLE jobs ADD CONSTRAINT jobs_status_check CHECK (status IN ('pending','running','done','failed','cancelled'));` and equivalents for the other columns.
- Reconcile `JobStatusCounts` with the real vocabulary (drop dead buckets, add `cancelled`).
- Validate `Priority` in `jobspec.Validate`.

## Related
- `internal/store/query/jobs.sql:262-272` (`JobStatusCounts`)
- `internal/store/migrations/000001_initial.up.sql:33, 42-43, 61`
- `internal/store/migrations/000006_scheduled_jobs.up.sql:8`
- `docs/backlog/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md` (performance side of the same query)
