DROP INDEX IF EXISTS ix_workers_agent_token_hash;
ALTER TABLE workers DROP COLUMN IF EXISTS agent_token_hash;

DROP INDEX IF EXISTS ix_agent_enrollments_token_hash;
DROP TABLE IF EXISTS agent_enrollments;
