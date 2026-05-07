-- Composite indexes supporting cursor pagination over (created_at, id).
-- All paginated list queries ORDER BY created_at DESC, id DESC and apply
-- (created_at, id) < (cursor_ts, cursor_id) when a cursor is present.
-- These indexes let Postgres serve those queries via Index Scan.

CREATE INDEX idx_jobs_created_id          ON jobs(created_at DESC, id DESC);
CREATE INDEX idx_jobs_status_created_id   ON jobs(status, created_at DESC, id DESC);
CREATE INDEX idx_jobs_sched_created_id    ON jobs(scheduled_job_id, created_at DESC, id DESC) WHERE scheduled_job_id IS NOT NULL;
CREATE INDEX idx_workers_created_id       ON workers(created_at DESC, id DESC);
CREATE INDEX idx_users_created_id         ON users(created_at DESC, id DESC);
CREATE INDEX idx_sched_jobs_created_id    ON scheduled_jobs(created_at DESC, id DESC);
CREATE INDEX idx_sched_jobs_owner_created ON scheduled_jobs(owner_id, created_at DESC, id DESC);
CREATE INDEX idx_agent_enr_created_id     ON agent_enrollments(created_at DESC, id DESC) WHERE consumed_at IS NULL;
CREATE INDEX idx_reservations_created_id  ON reservations(created_at DESC, id DESC);

-- Single-column indexes superseded by the composites above.
DROP INDEX IF EXISTS idx_jobs_status;            -- 000001_initial
DROP INDEX IF EXISTS idx_jobs_scheduled_job_id;  -- 000006_scheduled_jobs
DROP INDEX IF EXISTS idx_scheduled_jobs_owner;   -- 000006_scheduled_jobs
