---
title: No CHECK constraints on status vocabularies; JobStatusCounts counts statuses that are never written
type: bug
status: closed
created: 2026-06-10
closed: 2026-06-20
priority: medium
source: full-codebase review (2026-06-10)
---

## Resolution
Resolved 2026-06-20 via migration `000019_status_vocabulary_checks` plus a query
reconciliation and a jobspec validation. Three coupled changes:

1. **CHECK constraints** on all six free-TEXT columns, each set derived by enumerating
   every write site in code (not guessed):
   - `workers.status IN ('online','offline','stale','revoked')`
   - `jobs.status IN ('pending','running','done','failed','cancelled')`
   - `jobs.priority IN ('low','normal','high')`
   - `tasks.status IN ('pending','dispatched','running','done','failed','timed_out')`
   - `task_logs.stream IN ('stdout','stderr')`
   - `scheduled_jobs.overlap_policy IN ('skip','allow')`
   The migration normalizes any historically-drifted `jobs.priority`
   (`UPDATE ... SET priority='normal' WHERE priority NOT IN (...)`) before adding that
   one constraint; the other five columns have only bounded writers and need no cleanup.
   Down drops the six constraints.
2. **JobStatusCounts reconciled** to the real `jobs.status` vocabulary: the dead arms
   `running IN ('running','dispatched')`, `queued IN ('queued','pending')`, and
   `failed_24h IN ('failed','timed_out')` became `running = 'running'`, `queued = 'pending'`,
   and `failed_24h IN ('failed','cancelled')`. The public JSON field names are unchanged
   to avoid an API break; `queued` now counts `pending` (semantically correct).
3. **Priority validation** added to the shared `jobspec.Validate`
   (`internal/jobspec/jobspec.go`): accepts ``""``/`low`/`normal`/`high`, rejects typos.
   This is the single ingestion path (REST/CLI/MCP/schedrunner), so all paths are covered.

Verified by `internal/store/status_vocabulary_constraints_test.go` (per-column rejection +
down/up round-trip with the priority normalization) and the full store+api integration
suites (green). Phase-4 verify also fixed two now-invalid consumers: a speculative
`'preparing'`/`'prepare_failed'` task-status write in `store_test.go` (removed; never real
production values) and the `scripts/explain_sort_indexes/seed.go` benchmark seeder (dropped
`'critical'`/`'queued'`/`'dispatched'`). Spec:
`docs/superpowers/specs/2026-06-20-status-vocabulary-drift-design.md`.

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
