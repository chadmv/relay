-- name: CreateInvite :one
INSERT INTO invites (token_hash, email, created_by, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetInviteByTokenHash :one
SELECT * FROM invites WHERE token_hash = $1;

-- name: MarkInviteUsed :execrows
UPDATE invites
SET used_at = NOW(), used_by = $2
WHERE id = $1 AND used_at IS NULL;
