CREATE TABLE scheduled_jobs (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT         NOT NULL,
    owner_id        UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    cron_expr       TEXT         NOT NULL,
    timezone        TEXT         NOT NULL DEFAULT 'UTC',
    job_spec        JSONB        NOT NULL,
    overlap_policy  TEXT         NOT NULL DEFAULT 'skip',
    enabled         BOOLEAN      NOT NULL DEFAULT TRUE,
    next_run_at     TIMESTAMPTZ  NOT NULL,
    last_run_at     TIMESTAMPTZ,
    last_job_id     UUID         REFERENCES jobs(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_scheduled_jobs_next_run ON scheduled_jobs(next_run_at) WHERE enabled;
CREATE INDEX idx_scheduled_jobs_owner ON scheduled_jobs(owner_id);

ALTER TABLE jobs ADD COLUMN scheduled_job_id UUID
    REFERENCES scheduled_jobs(id) ON DELETE SET NULL;
CREATE INDEX idx_jobs_scheduled_job_id ON jobs(scheduled_job_id);
