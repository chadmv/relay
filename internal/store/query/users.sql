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

-- name: ListUsersPage :many
SELECT id, email, name, is_admin, created_at
FROM users
WHERE archived_at IS NULL
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountUsers :one
SELECT COUNT(*) FROM users WHERE archived_at IS NULL;

-- name: ListUsersIncludingArchivedPage :many
SELECT id, email, name, is_admin, created_at, archived_at
FROM users
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) < (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: CountUsersIncludingArchived :one
SELECT COUNT(*) FROM users;

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

-- name: ListUsersPageByCreatedAsc :many
SELECT id, email, name, is_admin, created_at
FROM users
WHERE archived_at IS NULL
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) > (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListUsersPageByNameDesc :many
SELECT id, email, name, is_admin, created_at
FROM users
WHERE archived_at IS NULL
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (name, id) < (sqlc.arg(cursor_v)::text, sqlc.arg(cursor_id)::uuid))
ORDER BY name DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListUsersPageByNameAsc :many
SELECT id, email, name, is_admin, created_at
FROM users
WHERE archived_at IS NULL
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (name, id) > (sqlc.arg(cursor_v)::text, sqlc.arg(cursor_id)::uuid))
ORDER BY name ASC, id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListUsersPageByEmailDesc :many
SELECT id, email, name, is_admin, created_at
FROM users
WHERE archived_at IS NULL
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (email, id) < (sqlc.arg(cursor_v)::text, sqlc.arg(cursor_id)::uuid))
ORDER BY email DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListUsersPageByEmailAsc :many
SELECT id, email, name, is_admin, created_at
FROM users
WHERE archived_at IS NULL
  AND (sqlc.arg(cursor_set)::bool = FALSE
       OR (email, id) > (sqlc.arg(cursor_v)::text, sqlc.arg(cursor_id)::uuid))
ORDER BY email ASC, id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListUsersIncludingArchivedPageByCreatedAsc :many
SELECT id, email, name, is_admin, created_at, archived_at
FROM users
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (created_at, id) > (sqlc.arg(cursor_ts)::timestamptz, sqlc.arg(cursor_id)::uuid))
ORDER BY created_at ASC, id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListUsersIncludingArchivedPageByNameDesc :many
SELECT id, email, name, is_admin, created_at, archived_at
FROM users
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (name, id) < (sqlc.arg(cursor_v)::text, sqlc.arg(cursor_id)::uuid))
ORDER BY name DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListUsersIncludingArchivedPageByNameAsc :many
SELECT id, email, name, is_admin, created_at, archived_at
FROM users
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (name, id) > (sqlc.arg(cursor_v)::text, sqlc.arg(cursor_id)::uuid))
ORDER BY name ASC, id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListUsersIncludingArchivedPageByEmailDesc :many
SELECT id, email, name, is_admin, created_at, archived_at
FROM users
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (email, id) < (sqlc.arg(cursor_v)::text, sqlc.arg(cursor_id)::uuid))
ORDER BY email DESC, id DESC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: ListUsersIncludingArchivedPageByEmailAsc :many
SELECT id, email, name, is_admin, created_at, archived_at
FROM users
WHERE (sqlc.arg(cursor_set)::bool = FALSE
       OR (email, id) > (sqlc.arg(cursor_v)::text, sqlc.arg(cursor_id)::uuid))
ORDER BY email ASC, id ASC
LIMIT sqlc.arg(page_limit)::int + 1;

-- name: GetUserEmailsByIDs :many
SELECT id, email FROM users WHERE id = ANY($1::uuid[]);
