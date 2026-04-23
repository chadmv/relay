DROP INDEX IF EXISTS idx_jobs_scheduled_job_id;
ALTER TABLE jobs DROP COLUMN IF EXISTS scheduled_job_id;
DROP INDEX IF EXISTS idx_scheduled_jobs_owner;
DROP INDEX IF EXISTS idx_scheduled_jobs_next_run;
DROP TABLE IF EXISTS scheduled_jobs;
