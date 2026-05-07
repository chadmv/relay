DROP INDEX IF EXISTS users_active_idx;
ALTER TABLE users DROP COLUMN archived_at;
