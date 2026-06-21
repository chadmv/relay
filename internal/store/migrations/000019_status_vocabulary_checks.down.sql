ALTER TABLE scheduled_jobs DROP CONSTRAINT IF EXISTS scheduled_jobs_overlap_policy_check;
ALTER TABLE task_logs      DROP CONSTRAINT IF EXISTS task_logs_stream_check;
ALTER TABLE tasks          DROP CONSTRAINT IF EXISTS tasks_status_check;
ALTER TABLE jobs           DROP CONSTRAINT IF EXISTS jobs_priority_check;
ALTER TABLE jobs           DROP CONSTRAINT IF EXISTS jobs_status_check;
ALTER TABLE workers        DROP CONSTRAINT IF EXISTS workers_status_check;
