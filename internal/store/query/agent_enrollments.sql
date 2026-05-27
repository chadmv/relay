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
