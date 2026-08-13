-- Supporting indexes for the two list endpoints added alongside this migration:
-- GET /v1/invites (keyset over created_at and expires_at) and
-- GET /v1/auth/tokens (keyset over created_at, scoped to one user).
--
-- Only DESC variants, matching 000013_paginated_sort_indexes.up.sql:7-10:
-- Postgres scans a btree backwards for the ascending arms, so a second ASC
-- index would be dead weight. Both sort columns here are NOT NULL on invites,
-- so no NULLS FIRST/LAST pair is needed (contrast 000013:15-16, which needed
-- one for the nullable workers.last_seen_at).
--
-- Plain CREATE INDEX, never CONCURRENTLY: golang-migrate wraps each migration
-- in a transaction and CONCURRENTLY cannot run inside one (000018:2-4).

CREATE INDEX idx_invites_created_id ON invites (created_at DESC, id DESC);
CREATE INDEX idx_invites_expires_id ON invites (expires_at DESC, id DESC);

-- api_tokens has had no user_id index since 000018:25 dropped the redundant
-- token_hash one, leaving only the UNIQUE(token_hash) btree. DeleteTokensForUser
-- and DeleteOtherTokensForUser (the password-change path) sequential-scan the
-- table today; this composite serves the new keyset list and both of them.
CREATE INDEX idx_api_tokens_user_created_id ON api_tokens (user_id, created_at DESC, id DESC);
