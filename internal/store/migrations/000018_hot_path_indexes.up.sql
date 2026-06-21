-- Hot-path indexes consolidation: add 5 supporting indexes for hot queries,
-- drop 4 redundant ones (3 duplicate a UNIQUE-constraint btree, 1 superseded
-- by a new composite). Plain CREATE/DROP INDEX (no CONCURRENTLY): golang-migrate
-- wraps each migration in a transaction, and CONCURRENTLY cannot run in one.

-- Add: missing supporting indexes for hot-path queries.
CREATE INDEX idx_task_deps_depends_on
  ON task_dependencies(depends_on_task_id);

CREATE INDEX idx_tasks_worker_active
  ON tasks(worker_id) WHERE status IN ('dispatched', 'running');

CREATE INDEX idx_task_logs_task_id_id
  ON task_logs(task_id, id);

CREATE INDEX idx_jobs_status_updated
  ON jobs(status, updated_at);

CREATE INDEX idx_workers_status_disabled
  ON workers(status, disabled_at);

-- Drop: redundant indexes (created after the new composite so nothing is
-- briefly unindexed). IF EXISTS keeps the drop idempotent.
DROP INDEX IF EXISTS idx_task_logs_task_id;           -- 000001:100, superseded by idx_task_logs_task_id_id
DROP INDEX IF EXISTS idx_api_tokens_token_hash;       -- 000001:101, dup of UNIQUE(token_hash)
DROP INDEX IF EXISTS ix_agent_enrollments_token_hash; -- 000005:11,  dup of UNIQUE(token_hash)
DROP INDEX IF EXISTS ix_workers_agent_token_hash;     -- 000005:14,  dup of UNIQUE(agent_token_hash)
