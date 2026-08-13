-- name: CreateToken :one
INSERT INTO api_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetTokenWithUser :one
SELECT
    t.id          AS token_id,
    t.user_id     AS token_user_id,
    t.token_hash,
    t.created_at  AS token_created_at,
    t.expires_at,
    u.id          AS user_id,
    u.name        AS user_name,
    u.email       AS user_email,
    u.is_admin    AS user_is_admin
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1
  AND u.archived_at IS NULL;

-- name: DeleteToken :exec
DELETE FROM api_tokens WHERE id = $1;

-- name: DeleteTokensForUser :exec
DELETE FROM api_tokens WHERE user_id = $1;

-- name: DeleteOtherTokensForUser :exec
DELETE FROM api_tokens WHERE user_id = $1 AND id <> $2;

-- name: ListActiveTokensForUserPage :many
-- One page of the caller's own API tokens, newest first.
--
-- The projection is EXPLICIT and omits token_hash. That is the endpoint's
-- security control: with the column absent from the SELECT, the generated row
-- type has no field for it, so returning it is a compile error rather than a
-- review miss. The handler has no reason to hold a hash at all - the
-- current-session flag is a UUID comparison against the token id BearerAuth
-- already resolved (internal/api/middleware.go:36-42), not a re-hash of the
-- presented credential.
--
-- user_id comes from the request context, never from the query string. There is
-- no user_id parameter on the endpoint and there must never be one.
--
-- The `expires_at IS NULL OR` arm is MANDATORY, not defensive noise. The column
-- is nullable (000001_initial.up.sql:18) and a NULL means "never expires":
-- BearerAuth rejects only on `Valid && Before(now)` (internal/api/middleware.go:32-35),
-- so a NULL-expiry token authenticates forever. A bare `expires_at > NOW()`
-- would hide exactly the most powerful credentials in the system from the one
-- screen a user goes to in order to find them. Keep this predicate identical in
-- all three statements here, including the count, or the pagination footer
-- states a number the caller cannot page to. See
-- TestListTokens_NeverExpiringTokenIsListed.
--
-- Expired rows are excluded because they cannot authenticate, nothing reaps
-- them (there is no janitor for api_tokens the way cmd/relay-server/main.go:253
-- reaps agent_enrollments), and there is no per-row revoke endpoint, so listing
-- them would render rows with no available action.
SELECT id, created_at, expires_at
FROM api_tokens
WHERE user_id = sqlc.arg(user_id)
  AND (expires_at IS NULL OR expires_at > NOW())
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListActiveTokensForUserPageByCreatedAsc :many
-- The ascending arm. parseSort strips a leading '-' before the allowlist check
-- (internal/api/pagination.go:178-181), so both directions of created_at are
-- reachable and each needs its own statement and dispatch arm.
--
-- expires_at is deliberately NOT a sort key: the column is nullable, so it
-- would need the NULLS LAST / NULLS FIRST index pair and the cursor-null
-- handling that 000013_paginated_sort_indexes.up.sql:15-16 needed for
-- workers.last_seen_at, for a list whose realistic length is single digits.
--
-- Same expiry predicate as ListActiveTokensForUserPage; see the note there.
SELECT id, created_at, expires_at
FROM api_tokens
WHERE user_id = sqlc.arg(user_id)
  AND (expires_at IS NULL OR expires_at > NOW())
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) > (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountActiveTokensForUser :one
-- The `total` for the sessions list, over the SAME predicate as the list
-- statements, so the pagination footer cannot state a number the caller cannot
-- page to.
--
-- Same expiry predicate as ListActiveTokensForUserPage; see the note there.
SELECT COUNT(*) FROM api_tokens
WHERE user_id = $1 AND (expires_at IS NULL OR expires_at > NOW());
