CREATE TABLE invites (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash  TEXT        NOT NULL UNIQUE,
    email       TEXT,
    created_by  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    used_by     UUID        REFERENCES users(id) ON DELETE SET NULL
);
