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
-- Both claims above were MEASURED, not reasoned: EXPLAIN over a multi-user
-- fixture during review showed Index Scan Backward on the DESC indexes for every
-- ASC arm, and idx_api_tokens_user_created_id serving both DeleteTokensForUser
-- and DeleteOtherTokensForUser. No committed test asserts a plan; re-measure
-- before assuming either still holds after a query change.
--
-- Plain CREATE INDEX, never CONCURRENTLY: golang-migrate wraps each migration
-- in a transaction and CONCURRENTLY cannot run inside one (000018:2-4).
-- Operational consequence, for whoever reads this during an incident: a plain
-- CREATE INDEX takes a SHARE lock on api_tokens - the authentication hot-path
-- table - for the length of the build, and golang-migrate holds it until the
-- whole migration commits. Reads are unaffected, so BearerAuth keeps
-- authenticating, but every WRITE to api_tokens blocks: no login can mint a
-- token and no logout can revoke one until the index is built. Startup
-- migrations run before the server serves, so on a large api_tokens this is a
-- login outage for the duration, not just a slow boot.

CREATE INDEX idx_invites_created_id ON invites (created_at DESC, id DESC);
CREATE INDEX idx_invites_expires_id ON invites (expires_at DESC, id DESC);

-- api_tokens has had no user_id index since 000018:25 dropped the redundant
-- token_hash one, leaving only the UNIQUE(token_hash) btree. DeleteTokensForUser
-- and DeleteOtherTokensForUser (the password-change path) sequential-scan the
-- table today; this composite serves the new keyset list and both of them.
CREATE INDEX idx_api_tokens_user_created_id ON api_tokens (user_id, created_at DESC, id DESC);
