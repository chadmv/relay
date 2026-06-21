-- Drop indexes added by the up migration.
DROP INDEX IF EXISTS idx_task_deps_depends_on;
DROP INDEX IF EXISTS idx_tasks_worker_active;
DROP INDEX IF EXISTS idx_task_logs_task_id_id;
DROP INDEX IF EXISTS idx_jobs_status_updated;
DROP INDEX IF EXISTS idx_workers_status_disabled;

-- Recreate indexes the up migration dropped, with their original definitions.
CREATE INDEX idx_task_logs_task_id ON task_logs(task_id);                       -- 000001:100
CREATE INDEX idx_api_tokens_token_hash ON api_tokens(token_hash);               -- 000001:101
CREATE INDEX ix_agent_enrollments_token_hash ON agent_enrollments(token_hash);  -- 000005:11
CREATE INDEX ix_workers_agent_token_hash
  ON workers(agent_token_hash) WHERE agent_token_hash IS NOT NULL;              -- 000005:14-15
