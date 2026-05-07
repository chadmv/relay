ALTER TABLE users ADD COLUMN archived_at TIMESTAMPTZ;
CREATE INDEX users_active_idx ON users (id) WHERE archived_at IS NULL;
