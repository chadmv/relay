DROP INDEX IF EXISTS idx_jobs_name_id;
DROP INDEX IF EXISTS idx_jobs_priority_id;
DROP INDEX IF EXISTS idx_jobs_status_id;
DROP INDEX IF EXISTS idx_jobs_updated_id;

DROP INDEX IF EXISTS idx_workers_name_id;
DROP INDEX IF EXISTS idx_workers_status_id;
DROP INDEX IF EXISTS idx_workers_last_seen_desc;
DROP INDEX IF EXISTS idx_workers_last_seen_asc;

DROP INDEX IF EXISTS idx_users_name_id;
DROP INDEX IF EXISTS idx_users_email_id;

DROP INDEX IF EXISTS idx_sched_jobs_name_id;
DROP INDEX IF EXISTS idx_sched_jobs_next_run_id;
DROP INDEX IF EXISTS idx_sched_jobs_updated_id;

DROP INDEX IF EXISTS idx_reservations_name_id;
DROP INDEX IF EXISTS idx_reservations_starts_desc;
DROP INDEX IF EXISTS idx_reservations_starts_asc;
DROP INDEX IF EXISTS idx_reservations_ends_desc;
DROP INDEX IF EXISTS idx_reservations_ends_asc;

DROP INDEX IF EXISTS idx_agent_enr_expires_id;
