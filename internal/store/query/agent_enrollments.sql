-- name: CreateAgentEnrollment :one
INSERT INTO agent_enrollments (token_hash, hostname_hint, created_by, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAgentEnrollmentByTokenHash :one
SELECT * FROM agent_enrollments WHERE token_hash = $1;

-- name: ConsumeAgentEnrollment :execrows
UPDATE agent_enrollments
SET consumed_at = NOW(), consumed_by = $2
WHERE id = $1 AND consumed_at IS NULL;

-- name: ListActiveAgentEnrollments :many
SELECT id, hostname_hint, created_by, created_at, expires_at
FROM agent_enrollments
WHERE consumed_at IS NULL AND expires_at > NOW()
ORDER BY created_at DESC;

-- name: DeleteExpiredAgentEnrollments :execrows
DELETE FROM agent_enrollments WHERE expires_at <= NOW() AND consumed_at IS NULL;

-- name: ListActiveAgentEnrollmentsPage :many
SELECT id, hostname_hint, created_by, created_at, expires_at
FROM agent_enrollments
WHERE consumed_at IS NULL
  AND expires_at > NOW()
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountActiveAgentEnrollments :one
SELECT COUNT(*) FROM agent_enrollments
WHERE consumed_at IS NULL AND expires_at > NOW();

-- name: ListActiveAgentEnrollmentsPageByCreatedAsc :many
SELECT id, hostname_hint, created_by, created_at, expires_at
FROM agent_enrollments
WHERE consumed_at IS NULL
  AND expires_at > NOW()
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) > (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListActiveAgentEnrollmentsPageByExpiresDesc :many
SELECT id, hostname_hint, created_by, created_at, expires_at
FROM agent_enrollments
WHERE consumed_at IS NULL
  AND expires_at > NOW()
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (expires_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY expires_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListActiveAgentEnrollmentsPageByExpiresAsc :many
SELECT id, hostname_hint, created_by, created_at, expires_at
FROM agent_enrollments
WHERE consumed_at IS NULL
  AND expires_at > NOW()
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (expires_at, id) > (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY expires_at ASC, id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ClearEnrollmentConsumerForWorker :execrows
-- Breaks the enrollment -> worker link so a worker row can be deleted. THE ONLY
-- STATEMENT PERMITTED TO SATISFY agent_enrollments.consumed_by's FOREIGN KEY,
-- which deliberately has NO ON DELETE ACTION (000005_agent_auth.up.sql:9).
--
-- THAT IS A DECISION, NOT AN OVERSIGHT (spec 5). A no-action FK fails CLOSED for
-- every future deleter - the planned TTL reaper included - with a loud SQLSTATE
-- 23503 that sends its author here. ON DELETE SET NULL would fail SILENT and
-- shred the link with no statement naming the act. If you arrived here from a
-- 23503, the guard is working: call this inside your delete transaction, before
-- the DELETE.
--
-- consumed_at is deliberately left alone, so the row still records that the token
-- was used and an unconsumed token stays distinguishable from a consumed one.
UPDATE agent_enrollments SET consumed_by = NULL WHERE consumed_by = $1;
