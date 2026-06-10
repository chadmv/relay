---
title: Add missing hot-path indexes and drop redundant ones
type: feature
status: open
created: 2026-06-10
priority: medium
source: full-codebase review (2026-06-10)
---

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
