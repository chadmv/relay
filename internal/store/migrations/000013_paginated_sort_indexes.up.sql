-- Composite indexes supporting cursor pagination with configurable ?sort=.
-- Each index covers ORDER BY <col> DESC, id DESC (asc scans use the same
-- index in reverse). Nullable-timestamp keys get both NULLS LAST and NULLS
-- FIRST variants so cursor pagination works in both directions.

-- jobs: name, priority, status, updated_at
CREATE INDEX idx_jobs_name_id     ON jobs (name DESC, id DESC);
CREATE INDEX idx_jobs_priority_id ON jobs (priority DESC, id DESC);
CREATE INDEX idx_jobs_status_id   ON jobs (status DESC, id DESC);
CREATE INDEX idx_jobs_updated_id  ON jobs (updated_at DESC, id DESC);

-- workers: name, status, last_seen_at (nullable)
CREATE INDEX idx_workers_name_id        ON workers (name DESC, id DESC);
CREATE INDEX idx_workers_status_id      ON workers (status DESC, id DESC);
CREATE INDEX idx_workers_last_seen_desc ON workers (last_seen_at DESC NULLS LAST, id DESC);
CREATE INDEX idx_workers_last_seen_asc  ON workers (last_seen_at ASC NULLS FIRST, id ASC);

-- users: name, email
CREATE INDEX idx_users_name_id  ON users (name DESC, id DESC);
CREATE INDEX idx_users_email_id ON users (email DESC, id DESC);

-- scheduled_jobs: name, next_run_at, updated_at
CREATE INDEX idx_sched_jobs_name_id     ON scheduled_jobs (name DESC, id DESC);
CREATE INDEX idx_sched_jobs_next_run_id ON scheduled_jobs (next_run_at DESC, id DESC);
CREATE INDEX idx_sched_jobs_updated_id  ON scheduled_jobs (updated_at DESC, id DESC);

-- reservations: name, starts_at (nullable), ends_at (nullable)
CREATE INDEX idx_reservations_name_id     ON reservations (name DESC, id DESC);
CREATE INDEX idx_reservations_starts_desc ON reservations (starts_at DESC NULLS LAST, id DESC);
CREATE INDEX idx_reservations_starts_asc  ON reservations (starts_at ASC NULLS FIRST, id ASC);
CREATE INDEX idx_reservations_ends_desc   ON reservations (ends_at DESC NULLS LAST, id DESC);
CREATE INDEX idx_reservations_ends_asc    ON reservations (ends_at ASC NULLS FIRST, id ASC);

-- agent_enrollments: expires_at
CREATE INDEX idx_agent_enr_expires_id ON agent_enrollments (expires_at DESC, id DESC);
