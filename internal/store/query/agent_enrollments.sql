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
