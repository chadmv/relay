CREATE TABLE agent_enrollments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash      TEXT NOT NULL UNIQUE,
    hostname_hint   TEXT,
    created_by      UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    consumed_at     TIMESTAMPTZ,
    consumed_by     UUID REFERENCES workers(id)
);
CREATE INDEX ix_agent_enrollments_token_hash ON agent_enrollments(token_hash);

ALTER TABLE workers ADD COLUMN agent_token_hash TEXT UNIQUE;
CREATE INDEX ix_workers_agent_token_hash
  ON workers(agent_token_hash) WHERE agent_token_hash IS NOT NULL;
