---
title: Add missing hot-path indexes and drop redundant ones
type: feature
status: closed
created: 2026-06-10
closed: 2026-06-20
priority: medium
source: full-codebase review (2026-06-10)
---

## Resolution
Resolved 2026-06-20 via migration `000018_hot_path_indexes` (up/down). One
indexes-only golang-migrate migration that ADDS 5 indexes and DROPS 4:

ADD:
- `idx_task_deps_depends_on ON task_dependencies(depends_on_task_id)` (FailDependentTasks CTE + FK cascade)
- `idx_tasks_worker_active ON tasks(worker_id) WHERE status IN ('dispatched','running')` (RequeueWorkerTasks, GetActiveTasksForWorker, ListGraceCandidates, CountActiveTasksByAllWorkers)
- `idx_task_logs_task_id_id ON task_logs(task_id, id)` (GetTaskLogsPage keyset paging)
- `idx_jobs_status_updated ON jobs(status, updated_at)` (folded in from `index-jobstatuscounts-full-table-scan`; covering, not partial - JobStatusCounts has no WHERE so a partial index would exclude rows it must count)
- `idx_workers_status_disabled ON workers(status, disabled_at)` (WorkerStatusCounts, the symmetric KPI)

DROP (each superseded by a UNIQUE-constraint btree or the new composite, confirmed
against every query that touches the columns):
- `idx_task_logs_task_id` (superseded by the new composite's leading column)
- `idx_api_tokens_token_hash`, `ix_agent_enrollments_token_hash`, `ix_workers_agent_token_hash`

Plain `CREATE INDEX` (no CONCURRENTLY - golang-migrate wraps each migration in a
transaction), matching existing index migrations. The down migration restores the 4
dropped indexes byte-faithfully (including the workers index's partial
`WHERE agent_token_hash IS NOT NULL`). Verified by `internal/store/hot_path_indexes_integration_test.go`
(index-set assertion + a real down->up round-trip); full store integration suite green (36/36).
Spec: `docs/superpowers/specs/2026-06-20-hot-path-indexes-design.md`. Also closes
`bug-2026-06-05-index-jobstatuscounts-full-table-scan` (folded in).

# Add missing hot-path indexes and drop redundant ones

## Summary
Several queries on hot paths have no supporting index, and three indexes duplicate UNIQUE constraints (pure write amplification). One consistency migration can fix all of it.

## Proposal
Add:
- `CREATE INDEX idx_task_deps_depends_on ON task_dependencies(depends_on_task_id);` - serves the `FailDependentTasks` CTE seed and recursive join, and the FK cascade on task deletion.
- `CREATE INDEX idx_tasks_worker_active ON tasks(worker_id) WHERE status IN ('dispatched', 'running');` - serves `RequeueWorkerTasks` (grace expiry), `GetActiveTasksForWorker` (reconnect), `ListGraceCandidates`, `CountActiveTasksByAllWorkers` (every dispatch cycle), and the unindexed `tasks.worker_id` FK.
- `CREATE INDEX idx_task_logs_task_id_id ON task_logs(task_id, id);` - serves `GetTaskLogsPage` keyset paging on the highest-volume table; drop the superseded `idx_task_logs_task_id`.

Drop (each duplicates an index already created by a UNIQUE constraint):
- `idx_api_tokens_token_hash` (000001:101)
- `ix_agent_enrollments_token_hash` (000005:11)
- `ix_workers_agent_token_hash` (000005:14-15)

## Related
- `internal/store/query/tasks.sql:64-69, 75-79, 106-122, 144-151, 166-172`
- `internal/store/migrations/000001_initial.up.sql:97-101`, `000005_agent_enrollments.up.sql`
- `docs/backlog/bug-2026-06-05-index-jobstatuscounts-full-table-scan.md` (same migration could carry it)
