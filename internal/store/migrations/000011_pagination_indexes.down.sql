-- Recreate single-column indexes that 000011 dropped, then drop the composites.

CREATE INDEX idx_jobs_status            ON jobs(status);
CREATE INDEX idx_jobs_scheduled_job_id  ON jobs(scheduled_job_id);
CREATE INDEX idx_scheduled_jobs_owner   ON scheduled_jobs(owner_id);

DROP INDEX IF EXISTS idx_jobs_created_id;
DROP INDEX IF EXISTS idx_jobs_status_created_id;
DROP INDEX IF EXISTS idx_jobs_sched_created_id;
DROP INDEX IF EXISTS idx_workers_created_id;
DROP INDEX IF EXISTS idx_users_created_id;
DROP INDEX IF EXISTS idx_sched_jobs_created_id;
DROP INDEX IF EXISTS idx_sched_jobs_owner_created;
DROP INDEX IF EXISTS idx_agent_enr_created_id;
DROP INDEX IF EXISTS idx_reservations_created_id;
