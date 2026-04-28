-- name: CreateUserWithPassword :one
INSERT INTO users (name, email, is_admin, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: SetPasswordHash :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: AdminExists :one
SELECT EXISTS(
    SELECT 1 FROM users WHERE is_admin = TRUE
) AS "exists";

-- name: PromoteUserToAdmin :exec
UPDATE users SET is_admin = TRUE WHERE id = $1;

-- name: ListUsers :many
SELECT id, email, name, is_admin, created_at
FROM users
ORDER BY created_at;
