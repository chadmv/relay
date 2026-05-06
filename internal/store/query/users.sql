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
WHERE archived_at IS NULL
ORDER BY created_at;

-- name: ListUsersIncludingArchived :many
SELECT id, email, name, is_admin, created_at, archived_at
FROM users
ORDER BY created_at;

-- name: GetUserByEmailPublic :one
SELECT id, email, name, is_admin, created_at, archived_at
FROM users WHERE email = $1;

-- name: UpdateUserName :one
UPDATE users SET name = $2 WHERE id = $1
RETURNING id, email, name, is_admin, created_at, archived_at;

-- name: ArchiveUser :one
UPDATE users SET archived_at = NOW()
WHERE id = $1 AND archived_at IS NULL
RETURNING id, email, name, is_admin, created_at, archived_at;

-- name: UnarchiveUser :one
UPDATE users SET archived_at = NULL
WHERE id = $1 AND archived_at IS NOT NULL
RETURNING id, email, name, is_admin, created_at, archived_at;

-- name: CountActiveAdmins :one
SELECT COUNT(*) FROM users
WHERE is_admin = TRUE AND archived_at IS NULL;

-- name: DeleteUserAPITokens :execrows
DELETE FROM api_tokens WHERE user_id = $1;
