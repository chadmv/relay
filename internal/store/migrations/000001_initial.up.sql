CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Users who submit jobs and own tokens
CREATE TABLE users (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    email      TEXT        NOT NULL UNIQUE,
    is_admin   BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Bearer tokens issued to users
CREATE TABLE api_tokens (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT        NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ
);

-- Worker nodes that execute tasks
CREATE TABLE workers (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT        NOT NULL,
    hostname     TEXT        NOT NULL UNIQUE,
    cpu_cores    INT         NOT NULL,
    ram_gb       INT         NOT NULL,
    gpu_count    INT         NOT NULL DEFAULT 0,
    gpu_model    TEXT        NOT NULL DEFAULT '',
    os           TEXT        NOT NULL,
    max_slots    INT         NOT NULL DEFAULT 1,
    labels       JSONB       NOT NULL DEFAULT '{}',
    status       TEXT        NOT NULL DEFAULT 'offline',
    last_seen_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Top-level units of work
CREATE TABLE jobs (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT        NOT NULL,
    priority     TEXT        NOT NULL DEFAULT 'normal',
    status       TEXT        NOT NULL DEFAULT 'pending',
    submitted_by UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT, -- intentional: preserve job history
    labels       JSONB       NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Individual executable units within a job
CREATE TABLE tasks (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id          UUID        NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    name            TEXT        NOT NULL,
    command         TEXT[]      NOT NULL,
    env             JSONB       NOT NULL DEFAULT '{}',
    requires        JSONB       NOT NULL DEFAULT '{}',
    timeout_seconds INT,
    retries         INT         NOT NULL DEFAULT 0,
    retry_count     INT         NOT NULL DEFAULT 0,
    status          TEXT        NOT NULL DEFAULT 'pending',
    worker_id       UUID        REFERENCES workers(id) ON DELETE SET NULL,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- DAG edges: task_id depends on depends_on_task_id
CREATE TABLE task_dependencies (
    task_id             UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on_task_id  UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, depends_on_task_id)
);

-- Captured stdout/stderr output from tasks
CREATE TABLE task_logs (
    id         BIGSERIAL   PRIMARY KEY,
    task_id    UUID        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    stream     TEXT        NOT NULL,
    content    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Admin-managed worker reservations
CREATE TABLE reservations (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    selector   JSONB       NOT NULL DEFAULT '{}',
    worker_ids UUID[]      NOT NULL DEFAULT '{}',
    user_id    UUID        REFERENCES users(id) ON DELETE CASCADE,
    project    TEXT,
    starts_at  TIMESTAMPTZ,
    ends_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tasks_job_id ON tasks(job_id);
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_task_logs_task_id ON task_logs(task_id);
CREATE INDEX idx_api_tokens_token_hash ON api_tokens(token_hash);
