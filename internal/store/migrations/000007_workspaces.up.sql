-- Add nullable source spec to tasks
ALTER TABLE tasks ADD COLUMN source JSONB;

-- Worker workspace inventory
CREATE TABLE worker_workspaces (
    worker_id     UUID NOT NULL REFERENCES workers(id) ON DELETE CASCADE,
    source_type   TEXT NOT NULL,
    source_key    TEXT NOT NULL,
    short_id      TEXT NOT NULL,
    baseline_hash TEXT NOT NULL,
    last_used_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (worker_id, source_type, source_key)
);
CREATE INDEX worker_workspaces_lookup_idx
    ON worker_workspaces (source_type, source_key, baseline_hash);
