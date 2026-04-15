-- name: CreateToken :one
INSERT INTO api_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetTokenWithUser :one
SELECT
    t.id          AS token_id,
    t.user_id,
    t.token_hash,
    t.created_at  AS token_created_at,
    t.expires_at,
    u.id          AS user_id,
    u.name        AS user_name,
    u.email       AS user_email,
    u.is_admin    AS user_is_admin
FROM api_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1;
